package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// readInput is intentionally bounded and rejects duplicate keys as well as
// unknown fields. Input files are administrator-owned lab artifacts, not the
// production consumer request-directory boundary.
func readInput(path string, out any) error {
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("cannot inspect input file")
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("input must be a regular file, not a symlink or device")
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open input file")
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return fmt.Errorf("input changed while opening")
	}
	after, err := os.Lstat(path)
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(after, opened) {
		return fmt.Errorf("input changed while opening")
	}
	body, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(body) > 65536 {
		return fmt.Errorf("input cannot be read or exceeds 64 KiB")
	}
	keyReader := json.NewDecoder(bytes.NewReader(body))
	if err = uniqueJSONValue(keyReader, 0); err != nil {
		return err
	}
	if _, err = keyReader.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON input")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil {
		return fmt.Errorf("input does not match the expected JSON schema")
	}
	return nil
}

// All three input schemas use lowercase ASCII field names. Requiring that
// spelling also rejects encoding/json's case-insensitive aliases (Host/host),
// which could otherwise overwrite one field without a literal duplicate key.
func inputFieldName(key string) bool {
	if key == "" || key[0] < 'a' || key[0] > 'z' {
		return false
	}
	for _, c := range key {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '_' {
			return false
		}
	}
	return true
}

func uniqueJSONValue(d *json.Decoder, depth int) error {
	if depth > 32 {
		return fmt.Errorf("JSON input is too deeply nested")
	}
	token, err := d.Token()
	if err != nil {
		return fmt.Errorf("invalid JSON input")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			token, err = d.Token()
			if err != nil {
				return fmt.Errorf("invalid JSON object")
			}
			key, ok := token.(string)
			if !ok || !inputFieldName(key) || keys[key] {
				return fmt.Errorf("duplicate or invalid JSON object key")
			}
			keys[key] = true
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return fmt.Errorf("invalid JSON object ending")
		}
	case '[':
		for d.More() {
			if err = uniqueJSONValue(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return fmt.Errorf("invalid JSON array ending")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}

func jsonArtifact(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return append(b, '\n'), err
}

func writeArtifacts(path string, files map[string][]byte) (err error) {
	if err = os.Mkdir(path, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	owned, err := root.Stat(".")
	if err != nil {
		return err
	}
	written := []string{}
	defer func() {
		if err == nil {
			return
		}
		for _, name := range written {
			_ = root.Remove(name)
		}
		// Only remove the directory we created, never a replacement path.
		if info, e := os.Lstat(path); e == nil && info.IsDir() && os.SameFile(owned, info) {
			_ = os.Remove(path)
		}
	}()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if filepath.Base(name) != name {
			return fmt.Errorf("invalid artifact name")
		}
		file, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		written = append(written, name)
		_, e = file.Write(files[name])
		if e == nil {
			e = file.Sync()
		}
		closeErr := file.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
