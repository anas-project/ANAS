package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrDirectoryCursorChanged = errors.New("mail directory cursor changed")

func (s Store) DirectoryCursor(ctx context.Context) (int64, error) {
	var seq int64
	err := s.DB.QueryRowContext(ctx, `SELECT sequence FROM mail.directory_cursor WHERE consumer='samba-mail-v1'`).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

// Commit only after successful reconciliation (or explicitly irrelevant events).
// The compare prevents an older consumer from overwriting a newer receipt.
func (s Store) SaveDirectoryCursor(ctx context.Context, expected, next int64) error {
	if expected < 0 || next < 0 {
		return ErrDirectoryCursorChanged
	}
	result, err := s.DB.ExecContext(ctx, `INSERT INTO mail.directory_cursor(consumer,sequence) SELECT 'samba-mail-v1',$2 WHERE $1::bigint=0
 ON CONFLICT(consumer) DO UPDATE SET sequence=$2,updated_at=clock_timestamp() WHERE mail.directory_cursor.sequence=$1`, expected, next)
	// Existing nonzero cursors need an UPDATE because the INSERT's initial-row
	// guard deliberately produces no row for a nonzero expected generation.
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		return nil
	}
	result, err = s.DB.ExecContext(ctx, `UPDATE mail.directory_cursor SET sequence=$2,updated_at=clock_timestamp() WHERE consumer='samba-mail-v1' AND sequence=$1`, expected, next)
	if err != nil {
		return err
	}
	n, err = result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrDirectoryCursorChanged
	}
	return nil
}
