package incushost

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxOSReleaseBytes = 64 << 10

type Release struct {
	ID       string `json:"id"`
	Version  string `json:"version"`
	Codename string `json:"codename,omitempty"`
}

var releaseKey = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,127}$`)

// ParseOSRelease reads data, never sources a shell. This installer deliberately
// rejects duplicate keys (including equal values), malformed unrelated fields,
// expansions and concatenated strings instead of guessing a privileged recipe.
// ID_LIKE is NOT used: derivatives require their own reviewed table row.
func ParseOSRelease(body []byte) (Release, error) {
	if len(body) == 0 || len(body) > MaxOSReleaseBytes || !utf8.Valid(body) {
		return Release{}, ErrInvalid
	}
	values := map[string]string{}
	for _, raw := range bytes.Split(body, []byte{'\n'}) {
		if len(raw) > 4096 {
			return Release{}, ErrInvalid
		}
		for _, r := range string(raw) {
			if unicode.IsControl(r) && r != '\t' {
				return Release{}, ErrInvalid
			}
		}
		line := strings.TrimSpace(string(raw))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, rawValue, ok := strings.Cut(line, "=")
		if !ok || !releaseKey.MatchString(key) {
			return Release{}, ErrInvalid
		}
		if _, exists := values[key]; exists {
			return Release{}, ErrInvalid
		}
		value, err := releaseValue(rawValue)
		if err != nil {
			return Release{}, err
		}
		values[key] = value
		if len(values) > 256 {
			return Release{}, ErrInvalid
		}
	}
	release := Release{ID: values["ID"], Version: values["VERSION_ID"], Codename: values["VERSION_CODENAME"]}
	if !distroID.MatchString(release.ID) || (release.Version != "" && !versionID.MatchString(release.Version)) || (release.Codename != "" && !versionID.MatchString(release.Codename)) {
		return Release{}, ErrInvalid
	}
	return release, nil
}

func releaseValue(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	quote := byte(0)
	if raw[0] == '\'' || raw[0] == '"' {
		quote = raw[0]
		if len(raw) < 2 || raw[len(raw)-1] != quote {
			return "", ErrInvalid
		}
		raw = raw[1 : len(raw)-1]
	}
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\\' && quote != '\'' {
			i++
			if i >= len(raw) || !strings.ContainsRune("\"\\$`", rune(raw[i])) {
				return "", ErrInvalid
			}
			out.WriteByte(raw[i])
			continue
		}
		if quote == '\'' {
			if c == '\'' {
				return "", ErrInvalid
			}
		} else if c == '$' || c == '`' || c == '"' || (quote == 0 && (c == '\'' || c == ' ' || c == '\t' || strings.ContainsRune(";|&()<>{}", rune(c)))) {
			return "", ErrInvalid
		}
		out.WriteByte(c)
	}
	return out.String(), nil
}
