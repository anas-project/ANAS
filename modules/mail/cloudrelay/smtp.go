package cloudrelay

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/anas-project/ANAS/internal/directorymail"
	"github.com/anas-project/ANAS/modules/mail/dms"
	smtp "github.com/emersion/go-smtp"
)

// smtpBackend reserves the final route under the same lock as Store, before
// Data returns success. Headers never participate in recipient selection.
type smtpBackend struct {
	store    *OriginalStore
	dataGate *smtpDataGate
}

type smtpSession struct {
	store      *OriginalStore
	dataGate   *smtpDataGate
	mu         sync.Mutex
	from       string
	recipients []string
}

var (
	smtpTemporary = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 3, 0}, Message: "temporary storage or routing failure"}
	smtpUnknown   = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "recipient unavailable"}
	smtpInvalid   = &smtp.SMTPError{Code: 553, EnhancedCode: smtp.EnhancedCode{5, 1, 3}, Message: "invalid address"}
	smtpOversize  = &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 3, 4}, Message: "message exceeds size limit"}
)

func (b smtpBackend) NewSession(*smtp.Conn) (smtp.Session, error) {
	if b.store == nil || b.store.ledger == nil {
		return nil, smtpTemporary
	}
	return &smtpSession{store: b.store, dataGate: b.dataGate}, nil
}

func (s *smtpSession) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.from, s.recipients = "", nil
}
func (s *smtpSession) Logout() error { s.Reset(); return nil }

func (s *smtpSession) Mail(from string, opts *smtp.MailOptions) error {
	s.Reset()
	if opts != nil && opts.Size > dms.IntakeMaxBytes {
		return smtpOversize
	}
	if from != "" {
		parsed, err := mail.ParseAddress(from)
		if err != nil || parsed.Name != "" || parsed.Address != from || len(from) > 254 || strings.ContainsAny(from, "\x00\r\n") {
			return smtpInvalid
		}
	}
	s.mu.Lock()
	s.from = from
	s.mu.Unlock()
	return nil
}

func (s *smtpSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	parts := strings.Split(to, "@")
	if len(parts) != 2 {
		return smtpInvalid
	}
	address, err := directorymail.Address(parts[0], parts[1])
	if err != nil {
		return smtpInvalid
	}
	state, err := s.store.ledger.Read()
	for attempt := 0; errors.Is(err, ErrRouteBusy) && attempt < 9; attempt++ {
		time.Sleep(25 * time.Millisecond)
		state, err = s.store.ledger.Read()
	}
	if err != nil {
		return smtpTemporary
	}
	for _, binding := range state.Bindings {
		if binding.ClosedSequence == nil && s.store.ledger.smtpAddress(binding.Route) == address {
			s.mu.Lock()
			defer s.mu.Unlock()
			for _, previous := range s.recipients {
				if previous == address {
					return nil
				}
			}
			if len(s.recipients) >= 100 {
				return smtpTemporary
			}
			s.recipients = append(s.recipients, address)
			return nil
		}
	}
	return smtpUnknown
}

func (s *smtpSession) Data(r io.Reader) error {
	if s.dataGate != nil {
		if !s.dataGate.start() {
			return smtpTemporary
		}
		defer s.dataGate.done()
	}
	s.mu.Lock()
	from := s.from
	recipients := append([]string(nil), s.recipients...)
	s.mu.Unlock()
	if len(recipients) == 0 {
		return smtpTemporary
	}
	if s.dataGate != nil {
		select {
		case s.dataGate.slots <- struct{}{}:
			defer func() { <-s.dataGate.slots }()
		default:
			return smtpTemporary
		}
	}
	original, err := io.ReadAll(io.LimitReader(r, dms.IntakeMaxBytes+1))
	if err != nil {
		if errors.Is(err, smtp.ErrDataTooLarge) {
			return smtpOversize
		}
		return smtpTemporary
	}
	if len(original) > dms.IntakeMaxBytes {
		return smtpOversize
	}
	if len(original) == 0 {
		return smtpInvalid
	}
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return smtpTemporary
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	id := hex.EncodeToString(random[:4]) + "-" + hex.EncodeToString(random[4:6]) + "-" + hex.EncodeToString(random[6:8]) + "-" + hex.EncodeToString(random[8:10]) + "-" + hex.EncodeToString(random[10:])
	_, err = s.store.Store(id, from, recipients, original)
	if err != nil {
		// A route may change after RCPT; this transaction must be retried,
		// never silently redirected or acknowledged without durable storage.
		return smtpTemporary
	}
	return nil
}
