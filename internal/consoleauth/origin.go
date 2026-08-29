package consoleauth

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// NormalizeOrigin validates a non-null HTTP(S) origin and returns its canonical
// scheme/host/port form without a trailing slash. Paths, credentials, queries,
// fragments, zones, and non-numeric ports are rejected.
func NormalizeOrigin(value string) (string, error) {
	if value == "" || value == "null" || strings.TrimSpace(value) != value {
		return "", errors.New("origin must be a non-null absolute HTTP(S) origin")
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Opaque != "" || parsed.User != nil || parsed.Host == "" {
		return "", errors.New("origin must be a non-null absolute HTTP(S) origin")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", errors.New("origin scheme must be http or https")
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawPath != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("origin must not contain a path, query, or fragment")
	}

	hostname := parsed.Hostname()
	if hostname == "" || strings.Contains(hostname, "%") {
		return "", errors.New("origin host is invalid")
	}
	if err := validateOriginAuthority(parsed.Host); err != nil {
		return "", err
	}
	port := parsed.Port()
	if port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 1 || portNumber > 65535 {
			return "", errors.New("origin port must be a number between 1 and 65535")
		}
		if scheme == "http" && portNumber == 80 || scheme == "https" && portNumber == 443 {
			port = ""
		}
	}

	if ip := net.ParseIP(hostname); ip != nil {
		hostname = ip.String()
	} else {
		var err error
		hostname, err = normalizeOriginDNSName(hostname)
		if err != nil {
			return "", err
		}
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return scheme + "://" + host, nil
}

func validateOriginAuthority(authority string) error {
	if strings.HasPrefix(authority, "[") {
		closing := strings.IndexByte(authority, ']')
		if closing < 0 {
			return errors.New("origin host is invalid")
		}
		suffix := authority[closing+1:]
		if suffix != "" && (!strings.HasPrefix(suffix, ":") || len(suffix) == 1) {
			return errors.New("origin port is invalid")
		}
		return nil
	}
	if strings.Contains(authority, ":") {
		_, port, err := net.SplitHostPort(authority)
		if err != nil || port == "" {
			return errors.New("origin port is invalid")
		}
	}
	return nil
}

func normalizeOriginDNSName(value string) (string, error) {
	value = strings.TrimSuffix(value, ".")
	if value == "" || len(value) > 253 {
		return "", errors.New("origin DNS host length is invalid")
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) < 1 || len(label) > 63 {
			return "", errors.New("origin DNS label length is invalid")
		}
		for index, char := range label {
			letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
			digit := char >= '0' && char <= '9'
			if !letter && !digit && !(char == '-' && index > 0 && index < len(label)-1) {
				return "", fmt.Errorf("origin host %q is not a valid ASCII DNS name", value)
			}
		}
	}
	return strings.ToLower(value), nil
}
