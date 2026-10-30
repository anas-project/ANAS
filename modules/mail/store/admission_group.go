package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrAdmissionGroupUnknown = errors.New("mail admission group is not pinned")
var ErrAdmissionGroupConflict = errors.New("mail admission group binding conflicts")

func admissionRole(role string) bool {
	return role == "application" || role == "all" || role == "administrator"
}

func (s Store) AdmissionGroup(ctx context.Context, role string) (string, error) {
	if !admissionRole(role) {
		return "", ErrAdmissionGroupConflict
	}
	var anchor string
	err := s.DB.QueryRowContext(ctx, `SELECT anchor FROM mail.admission_group WHERE role=$1`, role).Scan(&anchor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAdmissionGroupUnknown
	}
	if err != nil {
		return "", err
	}
	if !anchorPattern.MatchString(anchor) {
		return "", ErrAdmissionGroupConflict
	}
	return anchor, nil
}

// PinAdmissionGroup never repoints an existing role, including after the source
// group is deleted and a new group is created at the former DN.
func (s Store) PinAdmissionGroup(ctx context.Context, role, anchor string) error {
	if !admissionRole(role) || !anchorPattern.MatchString(anchor) {
		return ErrAdmissionGroupConflict
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO mail.admission_group(role,anchor) VALUES($1,$2) ON CONFLICT DO NOTHING`, role, anchor); err != nil {
		return err
	}
	var existing string
	if err = tx.QueryRowContext(ctx, `SELECT anchor FROM mail.admission_group WHERE role=$1`, role).Scan(&existing); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrAdmissionGroupConflict
		}
		return err
	}
	if existing != anchor {
		return ErrAdmissionGroupConflict
	}
	return tx.Commit()
}
