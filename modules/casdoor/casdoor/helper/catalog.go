package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
)

const catalogPrefix = "app-anas-catalog-"

func desiredCatalogObjects(data []byte) ([]string, []string, error) {
	var doc struct {
		Applications []struct{ Owner, Name string }
		Permissions  []struct{ Owner, Name string }
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, nil, err
	}
	apps, permissions := []string{}, []string{}
	for _, app := range doc.Applications {
		if app.Owner == "admin" && strings.HasPrefix(app.Name, catalogPrefix) {
			apps = append(apps, app.Name)
		}
	}
	for _, permission := range doc.Permissions {
		if permission.Owner == "anas" && strings.HasPrefix(permission.Name, "permission-"+catalogPrefix) {
			permissions = append(permissions, permission.Name)
		}
	}
	return apps, permissions, nil
}

func reconcileCatalog(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	apps, permissions, err := desiredCatalogObjects(data)
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", postgresDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM permission WHERE owner = $1 AND name LIKE $2 AND NOT (name = ANY($3))`, "anas", "permission-"+catalogPrefix+"%", pq.Array(permissions)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM application WHERE owner = $1 AND name LIKE $2 AND NOT (name = ANY($3))`, "admin", catalogPrefix+"%", pq.Array(apps)); err != nil {
		return err
	}
	return tx.Commit()
}
