package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	managedSigningOwner  = "admin"
	legacySigningName    = "anas-signing"
	managedSigningPrefix = "anas-signing-"
	portalApplication    = "app-built-in"
)

type managedSigningCertificate struct {
	certificate string
	privateKey  string
	displayName string
}

func reconcileCredential(action, kind string, input io.Reader) (string, error) {
	if action != "probe" && action != "reconcile" {
		return "", fmt.Errorf("unsupported credential action %q", action)
	}
	desiredBytes, err := io.ReadAll(io.LimitReader(input, 512*1024))
	if err != nil {
		return "", err
	}
	desired := strings.TrimSpace(string(desiredBytes))
	if desired == "" {
		return "", fmt.Errorf("desired credential is empty")
	}
	db, err := sql.Open("postgres", postgresDSN())
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		if action == "probe" {
			return "unavailable", nil
		}
		return "", fmt.Errorf("connect to Casdoor credential store: %w", err)
	}
	switch kind {
	case "signing-key":
		material, err := parseSigningMaterial(desired)
		if err != nil {
			return "", err
		}
		status, err := probeSigningMaterial(ctx, db, material, time.Now().UTC())
		if action == "probe" || err != nil || status == "match" {
			return status, err
		}
		if err := applySigningCredential(ctx, db, material, time.Now().UTC()); err != nil {
			return "", err
		}
		return "reconciled", nil
	case "portal-client-secret":
		status, err := probePortalSecret(ctx, db, desired)
		if action == "probe" || err != nil || status == "match" {
			return status, err
		}
		if err := applyPortalSecret(ctx, db, desired); err != nil {
			return "", err
		}
		return "reconciled", nil
	default:
		return "", fmt.Errorf("unsupported credential kind %q", kind)
	}
}

type queryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func probeSigningMaterial(ctx context.Context, db queryer, material signingMaterial, now time.Time) (string, error) {
	want := desiredSigningCertificates(material, now)
	currentName := managedSigningCertificateName(material.Certificate)
	if currentName == "" {
		return "", fmt.Errorf("managed signing certificate has no fingerprint")
	}
	rows, err := db.QueryContext(ctx,
		`SELECT name, certificate, private_key FROM cert
WHERE owner = $1 AND (name = $2 OR name LIKE $3)`,
		managedSigningOwner, legacySigningName, managedSigningPrefix+"%")
	if err != nil {
		return "unavailable", nil
	}
	defer rows.Close()
	seen := map[string]managedSigningCertificate{}
	for rows.Next() {
		var name, certificate, privateKey string
		if err := rows.Scan(&name, &certificate, &privateKey); err != nil {
			return "unavailable", nil
		}
		seen[name] = managedSigningCertificate{
			certificate: strings.TrimSpace(certificate), privateKey: strings.TrimSpace(privateKey),
		}
	}
	if err := rows.Err(); err != nil {
		return "unavailable", nil
	}
	// lib/pq cannot start the application query on the transaction's single
	// connection while the certificate result set is still open. This matters
	// during reconcile, where probeSigningMaterial receives *sql.Tx rather than
	// *sql.DB and therefore cannot obtain a second connection.
	if err := rows.Close(); err != nil {
		return "unavailable", nil
	}
	if legacy, exists := seen[legacySigningName]; exists && certificateIsDesired(legacy.certificate, want) {
		want[legacySigningName] = managedSigningCertificate{certificate: legacy.certificate}
	}
	if len(seen) != len(want) {
		return "mismatch", nil
	}
	for name, expected := range want {
		actual, exists := seen[name]
		if !exists || actual.certificate != strings.TrimSpace(expected.certificate) ||
			actual.privateKey != strings.TrimSpace(expected.privateKey) {
			return "mismatch", nil
		}
	}
	applicationRows, err := db.QueryContext(ctx,
		`SELECT name, cert FROM application
WHERE owner = $1 AND (name = $2 OR name LIKE 'app-anas-%')`,
		managedSigningOwner, portalApplication)
	if err != nil {
		return "unavailable", nil
	}
	defer applicationRows.Close()
	applications := 0
	for applicationRows.Next() {
		var name, certificateName string
		if err := applicationRows.Scan(&name, &certificateName); err != nil {
			return "unavailable", nil
		}
		applications++
		if !isManagedSigningApplication(name) || certificateName != currentName {
			return "mismatch", nil
		}
	}
	if err := applicationRows.Err(); err != nil || applications == 0 {
		return "mismatch", nil
	}
	return "match", nil
}

func desiredSigningCertificates(material signingMaterial, now time.Time) map[string]managedSigningCertificate {
	result := map[string]managedSigningCertificate{}
	if name := managedSigningCertificateName(material.Certificate); name != "" {
		result[name] = managedSigningCertificate{
			certificate: material.Certificate, privateKey: material.PrivateKey,
			displayName: "ANAS signing key",
		}
	}
	for _, trust := range material.TrustedCertificates {
		deadline, err := time.Parse(time.RFC3339, trust.RetainUntil)
		name := managedSigningCertificateName(trust.Certificate)
		if err == nil && deadline.After(now) && name != "" && trust.Certificate != material.Certificate {
			result[name] = managedSigningCertificate{
				certificate: trust.Certificate, displayName: "ANAS signing trust overlap",
			}
		}
	}
	return result
}

func certificateIsDesired(certificate string, desired map[string]managedSigningCertificate) bool {
	certificate = strings.TrimSpace(certificate)
	for _, item := range desired {
		if certificate == strings.TrimSpace(item.certificate) {
			return true
		}
	}
	return false
}

func applySigningCredential(ctx context.Context, db *sql.DB, material signingMaterial, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	want := desiredSigningCertificates(material, now)
	var legacyCertificate string
	legacyErr := tx.QueryRowContext(ctx,
		`SELECT certificate FROM cert WHERE owner = $1 AND name = $2`,
		managedSigningOwner, legacySigningName).Scan(&legacyCertificate)
	if legacyErr != nil && legacyErr != sql.ErrNoRows {
		return fmt.Errorf("read legacy Casdoor signing certificate: %w", legacyErr)
	}
	if legacyErr == nil && certificateIsDesired(legacyCertificate, want) {
		want[legacySigningName] = managedSigningCertificate{
			certificate: strings.TrimSpace(legacyCertificate), displayName: "ANAS legacy signing trust alias",
		}
	}
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		item := want[name]
		result, err := tx.ExecContext(ctx, `
INSERT INTO cert
SELECT (jsonb_populate_record(NULL::cert,
  to_jsonb(source) || jsonb_build_object(
    'name', $1::text, 'display_name', $2::text,
    'certificate', $3::text, 'private_key', $4::text))).*
FROM (
  SELECT * FROM cert
  WHERE owner = $5 AND (name = $6 OR name LIKE $7)
  ORDER BY CASE WHEN name = $6 THEN 0 ELSE 1 END
  LIMIT 1
) AS source
ON CONFLICT (owner, name) DO UPDATE
SET display_name = EXCLUDED.display_name,
    certificate = EXCLUDED.certificate,
    private_key = EXCLUDED.private_key`, name, item.displayName, item.certificate, item.privateKey,
			managedSigningOwner, legacySigningName, managedSigningPrefix+"%")
		if err != nil {
			return fmt.Errorf("store Casdoor signing certificate %s: %w", name, err)
		}
		if rows, _ := result.RowsAffected(); rows != 1 {
			return fmt.Errorf("store Casdoor signing certificate %s affected %d rows", name, rows)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM cert
WHERE owner = $1 AND (name = $2 OR name LIKE $3) AND NOT (name = ANY($4))`,
		managedSigningOwner, legacySigningName, managedSigningPrefix+"%", pq.Array(names)); err != nil {
		return fmt.Errorf("retire expired Casdoor signing trust: %w", err)
	}
	currentName := managedSigningCertificateName(material.Certificate)
	result, err := tx.ExecContext(ctx,
		`UPDATE application SET cert = $1
WHERE owner = $2 AND (name = $3 OR name LIKE 'app-anas-%')`,
		currentName, managedSigningOwner, portalApplication)
	if err != nil {
		return fmt.Errorf("update Casdoor application signing references: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows < 1 {
		return fmt.Errorf("update Casdoor application signing references affected %d rows", rows)
	}
	status, err := probeSigningMaterial(ctx, tx, material, now)
	if err != nil || status != "match" {
		return fmt.Errorf("verify Casdoor signing key: status=%s error=%v", status, err)
	}
	return tx.Commit()
}

func probePortalSecret(ctx context.Context, db queryer, desired string) (string, error) {
	var current string
	err := db.QueryRowContext(ctx,
		`SELECT client_secret FROM application WHERE owner = $1 AND name = $2`,
		managedSigningOwner, portalApplication).Scan(&current)
	if err == sql.ErrNoRows {
		return "missing", nil
	}
	if err != nil {
		return "unavailable", nil
	}
	if current == desired {
		return "match", nil
	}
	return "mismatch", nil
}

func applyPortalSecret(ctx context.Context, db *sql.DB, desired string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx,
		`UPDATE application SET client_secret = $1 WHERE owner = $2 AND name = $3`,
		desired, managedSigningOwner, portalApplication)
	if err != nil {
		return fmt.Errorf("update Casdoor portal client secret: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return fmt.Errorf("update Casdoor portal client secret affected %d rows", rows)
	}
	status, err := probePortalSecret(ctx, tx, desired)
	if err != nil || status != "match" {
		return fmt.Errorf("verify Casdoor portal client secret: status=%s error=%v", status, err)
	}
	return tx.Commit()
}
