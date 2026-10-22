package main

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func validateServiceAPI(env map[string]string) error {
	origin, workspace, directory := env["USER_PORTAL_SERVICE_API_URL"], env["USER_PORTAL_SERVICE_API_WORKSPACE"], env["USER_PORTAL_SERVICE_API_CREDENTIALS_DIR"]
	if origin == "" && workspace == "" && directory == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`).MatchString(workspace) {
		return errors.New("service API requires an HTTPS origin and explicit workspace")
	}
	if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || strings.ContainsAny(directory, ":\r\n$") {
		return errors.New("service API requires an absolute dedicated credentials directory")
	}
	for _, broad := range []string{"/", "/run", "/var", "/var/lib", "/etc", "/tmp", "/home", "/root"} {
		if directory == broad {
			return errors.New("service API must mount only the client's dedicated credentials directory")
		}
	}
	return nil
}

// The disabled deployment needs only an empty mount. Never create the enabled
// client's directory: anasd owns its permissions, marker and token lifecycle.
func prepareDisabledServiceAPIDirectory(env map[string]string) error {
	if env["USER_PORTAL_SERVICE_API_URL"] != "" || env["DATA_PATH"] == "" {
		return nil
	}
	if !filepath.IsAbs(env["DATA_PATH"]) {
		return errors.New("portal data path must be absolute")
	}
	if err := os.MkdirAll(filepath.Join(env["DATA_PATH"], "user_portal", "service-client"), 0700); err != nil {
		return errors.New("cannot prepare disabled service API mount")
	}
	return nil
}
