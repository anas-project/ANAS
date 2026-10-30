package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/anas-project/ANAS/modules/mail/identity"
)

var ErrCompanyDirectoryUnavailable = errors.New("mail company directory unavailable")

type CompanyDirectory struct {
	Data        identity.CompanySnapshot
	Fingerprint string
	RefreshedAt time.Time
}

func (s Store) SaveCompanyDirectory(ctx context.Context, snapshot identity.CompanySnapshot) (string, error) {
	if err := identity.ValidateCompanySnapshot(snapshot); err != nil {
		return "", ErrCompanyDirectoryUnavailable
	}
	raw, err := json.Marshal(snapshot)
	if err != nil || len(raw) > 8<<20 {
		return "", ErrCompanyDirectoryUnavailable
	}
	sum := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(sum[:])
	_, err = s.DB.ExecContext(ctx, `INSERT INTO mail.company_directory(source,fingerprint,snapshot) VALUES('company',$1,$2::jsonb)
 ON CONFLICT(source) DO UPDATE SET fingerprint=EXCLUDED.fingerprint,snapshot=EXCLUDED.snapshot,refreshed_at=clock_timestamp()`, fingerprint, raw)
	return fingerprint, err
}
func (s Store) CompanyDirectory(ctx context.Context) (CompanyDirectory, error) {
	var result CompanyDirectory
	var raw []byte
	err := s.DB.QueryRowContext(ctx, `SELECT fingerprint,snapshot,refreshed_at FROM mail.company_directory WHERE source='company'`).Scan(&result.Fingerprint, &raw, &result.RefreshedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrCompanyDirectoryUnavailable
	}
	if err != nil {
		return result, err
	}
	if len(raw) > 8<<20 {
		return result, ErrCompanyDirectoryUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result.Data) != nil || identity.ValidateCompanySnapshot(result.Data) != nil {
		return CompanyDirectory{}, ErrCompanyDirectoryUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return CompanyDirectory{}, ErrCompanyDirectoryUnavailable
	}
	canonical, err := json.Marshal(result.Data)
	if err != nil {
		return CompanyDirectory{}, ErrCompanyDirectoryUnavailable
	}
	sum := sha256.Sum256(canonical)
	if hex.EncodeToString(sum[:]) != result.Fingerprint {
		return CompanyDirectory{}, ErrCompanyDirectoryUnavailable
	}
	return result, nil
}
