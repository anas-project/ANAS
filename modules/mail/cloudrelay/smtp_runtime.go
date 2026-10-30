package cloudrelay

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"time"

	"github.com/anas-project/ANAS/modules/mail/dms"
	smtp "github.com/emersion/go-smtp"
	"golang.org/x/net/netutil"
)

const smtpConnectionLimit = 8

// BDAT invokes Session.Data in a separate library goroutine. Track it
// separately so the source lock is not released while storage is still active.
type smtpDataGate struct {
	mu       sync.Mutex
	stopping bool
	active   sync.WaitGroup
	slots    chan struct{}
}

func (g *smtpDataGate) start() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping {
		return false
	}
	g.active.Add(1)
	return true
}
func (g *smtpDataGate) done() { g.active.Done() }
func (g *smtpDataGate) stop() {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()
	g.active.Wait()
}

// trackedListener is owned by a single runtime. Closing underlying sockets
// interrupts SMTP reads without concurrently invoking the SMTP session's
// Logout method while its handler is still processing a transaction.
type trackedListener struct {
	net.Listener
	mu     sync.Mutex
	closed bool
	conns  map[*trackedConn]struct{}
}

type trackedConn struct {
	net.Conn
	parent *trackedListener
	once   sync.Once
}

func (l *trackedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		_ = c.Close()
		return nil, net.ErrClosed
	}
	tracked := &trackedConn{Conn: c, parent: l}
	l.conns[tracked] = struct{}{}
	return tracked, nil
}

func (l *trackedListener) Close() error {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	return l.Listener.Close()
}

func (l *trackedListener) closeConnections() {
	l.mu.Lock()
	conns := make([]*trackedConn, 0, len(l.conns))
	for c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()
	for _, c := range conns {
		_ = c.Conn.Close()
	}
}

func (c *trackedConn) Close() error {
	var err error
	c.once.Do(func() {
		err = c.Conn.Close()
		c.parent.mu.Lock()
		delete(c.parent.conns, c)
		c.parent.mu.Unlock()
	})
	return err
}

func newSMTPRuntime(store *OriginalStore, hostname string, cert tls.Certificate, listener net.Listener) (*smtp.Server, net.Listener, *trackedListener, *smtpDataGate) {
	tracked := &trackedListener{Listener: listener, conns: make(map[*trackedConn]struct{})}
	limited := netutil.LimitListener(tracked, smtpConnectionLimit)
	gate := &smtpDataGate{slots: make(chan struct{}, 1)}
	server := smtp.NewServer(smtpBackend{store: store, dataGate: gate})
	server.Domain = hostname
	server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	server.MaxRecipients = 100
	// go-smtp's DATA reader needs one extra byte of allowance to recognize the
	// terminating dot at exactly IntakeMaxBytes. Mail and Data enforce our
	// actual limit; clients declaring the extra byte are rejected at MAIL.
	server.MaxMessageBytes = dms.IntakeMaxBytes + 1
	server.ReadTimeout = 2 * time.Minute
	server.WriteTimeout = 30 * time.Second
	server.ErrorLog = discardSMTPLog{}
	return server, limited, tracked, gate
}

// Do not log untrusted SMTP input, email addresses or TLS handshake errors.
type discardSMTPLog struct{}

func (discardSMTPLog) Printf(string, ...interface{}) {}
func (discardSMTPLog) Println(...interface{})        {}

var _ io.Closer = (*trackedConn)(nil)
