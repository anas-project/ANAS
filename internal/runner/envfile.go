package runner

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// globalEnvFile is the deployment-wide environment, written beside the
// rendered modules rather than inside one of them. Artifact start, stop and
// rollback read it to recover what the release was built with. The leading dot
// keeps it out of the directory scan that finds modules by their subdirectory.
const globalEnvFile = ".global.env"

// writeEnv writes a per-module .env file. Values are quoted so that docker
// compose's dotenv parser and parseEnvFile both read back the exact original
// value; see quoteEnv for the rules.
func writeEnv(path string, env map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return err
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := bufio.NewWriter(f)
	for _, k := range keys {
		fmt.Fprintf(w, "%s=%s\n", k, quoteEnv(env[k]))
	}
	return w.Flush()
}

// quoteEnv encodes a value for a dotenv file. Unlike shell quoting, dotenv
// has no adjacent-string concatenation, so values are written as exactly one
// of three forms:
//
//   - plain: safe characters only, written as-is
//   - single-quoted: literal text, used when the value contains specials but
//     no single quote or line break (no interpolation inside single quotes)
//   - double-quoted: \\ \" \n \r escapes plus $$ for a literal dollar so
//     compose interpolation cannot rewrite the value
func quoteEnv(v string) string {
	if v == "" {
		return `""`
	}
	if isPlainEnvValue(v) {
		return v
	}
	if !strings.ContainsAny(v, "'\n\r") {
		return "'" + v + "'"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range v {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '$':
			b.WriteString("$$")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func isPlainEnvValue(v string) bool {
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("_.-/:@+,=", r):
		default:
			return false
		}
	}
	return true
}

// parseEnvFile reads a .env file previously produced by writeEnv. It is the
// runner-side inverse of quoteEnv and is used when starting from a rendered
// release without recalculating the environment.
func parseEnvFile(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			return nil, fmt.Errorf("%s:%d: invalid env line", path, i+1)
		}
		value, err := unquoteEnv(line[eq+1:])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, i+1, err)
		}
		env[line[:eq]] = value
	}
	return env, nil
}

func unquoteEnv(raw string) (string, error) {
	if len(raw) >= 2 && strings.HasPrefix(raw, "'") && strings.HasSuffix(raw, "'") {
		inner := raw[1 : len(raw)-1]
		if strings.Contains(inner, "'") {
			return "", fmt.Errorf("invalid single-quoted value %q", raw)
		}
		return inner, nil
	}
	if len(raw) >= 2 && strings.HasPrefix(raw, `"`) && strings.HasSuffix(raw, `"`) {
		inner := raw[1 : len(raw)-1]
		var b strings.Builder
		for i := 0; i < len(inner); i++ {
			c := inner[i]
			switch c {
			case '\\':
				if i+1 >= len(inner) {
					return "", fmt.Errorf("trailing backslash in %q", raw)
				}
				i++
				switch inner[i] {
				case '\\':
					b.WriteByte('\\')
				case '"':
					b.WriteByte('"')
				case 'n':
					b.WriteByte('\n')
				case 'r':
					b.WriteByte('\r')
				default:
					return "", fmt.Errorf("unsupported escape \\%c in %q", inner[i], raw)
				}
			case '$':
				if i+1 < len(inner) && inner[i+1] == '$' {
					i++
				}
				b.WriteByte('$')
			default:
				b.WriteByte(c)
			}
		}
		return b.String(), nil
	}
	return raw, nil
}
