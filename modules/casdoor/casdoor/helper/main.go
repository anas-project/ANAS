package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"golang.org/x/crypto/bcrypt"
)

const passwordPlaceholder = "__ANAS_BREAK_GLASS_PASSWORD_HASH__"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: casdoor-helper <render-init|set-password|healthcheck|exec-as|directory-watch>")
	}
	switch args[0] {
	case "render-init":
		if len(args) != 4 {
			return fmt.Errorf("render-init requires template, password file, and output")
		}
		return renderInit(args[1], args[2], args[3])
	case "set-password":
		if len(args) != 3 {
			return fmt.Errorf("set-password requires owner and username")
		}
		return setPassword(args[1], args[2], os.Stdin)
	case "healthcheck":
		return healthcheck()
	case "exec-as":
		if len(args) < 4 {
			return fmt.Errorf("exec-as requires uid, gid, and a command")
		}
		return execAs(args[1], args[2], args[3:])
	case "directory-watch":
		return runDirectoryWatch(args[1:])
	default:
		return fmt.Errorf("unsupported command %q", args[0])
	}
}

func execAs(uidText, gidText string, command []string) error {
	uid, err := strconv.Atoi(uidText)
	if err != nil || uid < 1 {
		return fmt.Errorf("invalid uid %q", uidText)
	}
	gid, err := strconv.Atoi(gidText)
	if err != nil || gid < 1 {
		return fmt.Errorf("invalid gid %q", gidText)
	}
	if err := syscall.Setgroups(nil); err != nil {
		return fmt.Errorf("clear supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	return syscall.Exec(command[0], command, os.Environ())
}

func renderInit(templatePath, passwordPath, outputPath string) error {
	template, err := os.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("read init template: %w", err)
	}
	password, err := readSecretFile(passwordPath)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash break-glass password: %w", err)
	}
	if bytes.Count(template, []byte(passwordPlaceholder)) != 1 {
		return fmt.Errorf("init template must contain exactly one password placeholder")
	}
	rendered := bytes.Replace(template, []byte(passwordPlaceholder), hash, 1)
	var doc any
	if err := json.Unmarshal(rendered, &doc); err != nil {
		return fmt.Errorf("validate rendered init data: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0700); err != nil {
		return err
	}
	return os.WriteFile(outputPath, rendered, 0600)
}

func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read break-glass password: %w", err)
	}
	value := strings.TrimRight(string(b), "\r\n")
	if value == "" {
		return "", fmt.Errorf("break-glass password is empty")
	}
	return value, nil
}

func setPassword(owner, username string, in io.Reader) error {
	b, err := io.ReadAll(io.LimitReader(in, 16*1024))
	if err != nil {
		return err
	}
	password := strings.TrimRight(string(b), "\r\n")
	if password == "" {
		return fmt.Errorf("candidate password is empty")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", postgresDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	ctxTimeout := 15 * time.Second
	db.SetConnMaxLifetime(ctxTimeout)
	result, err := db.Exec(`UPDATE "user" SET password = $1 WHERE owner = $2 AND name = $3`, string(hash), owner, username)
	if err != nil {
		return fmt.Errorf("update Casdoor administrator: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return fmt.Errorf("update Casdoor administrator affected %d rows", rows)
	}
	var stored string
	if err := db.QueryRow(`SELECT password FROM "user" WHERE owner = $1 AND name = $2`, owner, username).Scan(&stored); err != nil {
		return fmt.Errorf("verify Casdoor administrator: %w", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(stored), []byte(password)); err != nil {
		return fmt.Errorf("verify Casdoor administrator password: %w", err)
	}
	return nil
}

func postgresDSN() string {
	return fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		os.Getenv("CASDOOR_DB_HOST"), os.Getenv("CASDOOR_DB_PORT"),
		os.Getenv("CASDOOR_DB_USERNAME"), os.Getenv("CASDOOR_DB_PASSWORD"),
		os.Getenv("CASDOOR_DB_NAME"))
}

func healthcheck() error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8000/api/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("Casdoor health endpoint returned %s", resp.Status)
	}
	return nil
}
