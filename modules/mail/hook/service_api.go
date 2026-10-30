package main

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var serviceWorkspace = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validateServiceAPI(e map[string]string) error {
	origin, workspace, dir, socketDir := e["MAIL_SERVICE_API_URL"], e["MAIL_SERVICE_API_WORKSPACE"], e["MAIL_SERVICE_API_CREDENTIALS_DIR"], e["MAIL_SERVICE_API_SOCKET_DIR"]
	if origin == "" && workspace == "" && dir == "" && socketDir == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(origin, "\x00\r\n") || !serviceWorkspace.MatchString(workspace) {
		return errors.New("mail service API requires a direct HTTPS origin and workspace")
	}
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || strings.ContainsAny(dir, ":\r\n$") {
		return errors.New("mail service API requires a dedicated absolute credential directory")
	}
	for _, broad := range []string{"/", "/run", "/var", "/var/lib", "/etc", "/tmp", "/home", "/root"} {
		if dir == broad {
			return errors.New("mail service API must mount only its dedicated credential directory")
		}
	}
	if socketDir != "" {
		if !filepath.IsAbs(socketDir) || filepath.Clean(socketDir) != socketDir || strings.ContainsAny(socketDir, "\x00:\r\n$") || socketDir == "/" || socketDir == "/run" || socketDir == "/run/anas" || socketDir == "/var" || socketDir == "/var/lib" || socketDir == "/etc" || socketDir == "/home" || socketDir == "/root" || socketDir == "/tmp" || socketDir == dir || strings.HasPrefix(socketDir, dir+string(filepath.Separator)) || strings.HasPrefix(dir, socketDir+string(filepath.Separator)) {
			return errors.New("mail service API requires a separate dedicated socket directory")
		}
	}
	return nil
}

func prepareDisabledServiceAPIDirectory(e map[string]string) error {
	if e["MAIL_SERVICE_API_URL"] != "" || e["DATA_PATH"] == "" {
		return nil
	}
	if !filepath.IsAbs(e["DATA_PATH"]) || os.MkdirAll(filepath.Join(e["DATA_PATH"], "mail", "service-client"), 0700) != nil {
		return errors.New("cannot prepare disabled mail service API mount")
	}
	return nil
}
