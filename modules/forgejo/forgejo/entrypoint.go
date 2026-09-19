package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	forgejoBinary      = "/usr/local/bin/forgejo"
	upstreamEntrypoint = "/usr/local/bin/docker-entrypoint.sh"
	forgejoData        = "/var/lib/gitea"
	forgejoAPI         = "http://127.0.0.1:3000"
	forgejoUID         = 1000
	forgejoGID         = 1000
)

type localAdminInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type oidcInput struct {
	Name         string `json:"name"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DiscoveryURL string `json:"discovery_url"`
	AdminGroup   string `json:"admin_group"`
}

type apiUser struct {
	Login   string `json:"login"`
	IsAdmin bool   `json:"is_admin"`
}

var (
	httpClient        = &http.Client{Timeout: 10 * time.Second}
	runForgejoCommand = func(args ...string) ([]byte, error) {
		return exec.Command(forgejoBinary, args...).CombinedOutput()
	}
	randomPasswordPattern = regexp.MustCompile(`generated random password is '([^']+)'`)
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			if err := dropPrivileges(forgejoUID, forgejoGID); err != nil {
				fatal(err)
			}
			if err := checkHealth(forgejoAPI); err != nil {
				fatal(err)
			}
			return
		case "local-admin":
			var input localAdminInput
			if err := decodeInput(&input); err != nil {
				fatal(err)
			}
			if err := ensureLocalAdmin(input, forgejoAPI); err != nil {
				fatal(err)
			}
			return
		case "oidc":
			var input oidcInput
			if err := decodeInput(&input); err != nil {
				fatal(err)
			}
			if err := ensureOIDC(input); err != nil {
				fatal(err)
			}
			return
		}
	}

	if err := prepareData(forgejoData, forgejoUID, forgejoGID); err != nil {
		fatal(err)
	}
	if err := dropPrivileges(forgejoUID, forgejoGID); err != nil {
		fatal(err)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{forgejoBinary, "web"}
	}
	argv := append([]string{upstreamEntrypoint}, args...)
	if err := syscall.Exec(upstreamEntrypoint, argv, os.Environ()); err != nil {
		fatal(fmt.Errorf("exec Forgejo entrypoint: %w", err))
	}
}

func decodeInput(value any) error {
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode reconciliation input: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("reconciliation input contains trailing data")
	}
	return nil
}

func prepareData(root string, uid, gid int) error {
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("create Forgejo data directory: %w", err)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect Forgejo data directory: %w", err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) == uid && int(stat.Gid) == gid {
		return nil
	}
	if err := chownDataTree(root, uid, gid); err != nil {
		return fmt.Errorf("prepare Forgejo data ownership: %w", err)
	}
	return nil
}

func chownDataTree(root string, uid, gid int) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// WalkDir and Lchown deliberately avoid following restored symlinks.
		if err := os.Lchown(path, uid, gid); err != nil {
			return fmt.Errorf("set Forgejo data ownership for %s: %w", path, err)
		}
		return nil
	})
}

func dropPrivileges(uid, gid int) error {
	if os.Geteuid() != 0 {
		return nil
	}
	if err := syscall.Setgroups([]int{gid}); err != nil {
		return fmt.Errorf("set supplementary groups: %w", err)
	}
	if err := syscall.Setgid(gid); err != nil {
		return fmt.Errorf("set gid: %w", err)
	}
	if err := syscall.Setuid(uid); err != nil {
		return fmt.Errorf("set uid: %w", err)
	}
	syscall.Umask(0o027)
	return nil
}

func checkHealth(baseURL string) error {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/-/healthcheck", nil)
	if err != nil {
		return err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("Forgejo health request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Forgejo health status %d", response.StatusCode)
	}
	return nil
}

func ensureLocalAdmin(input localAdminInput, baseURL string) error {
	if input.Username == "" || input.Email == "" || input.Password == "" {
		return errors.New("local recovery input is incomplete")
	}
	user, authenticated, err := authenticate(baseURL, input.Username, input.Password)
	if err != nil {
		return err
	}
	if authenticated {
		if user.Login != input.Username || !user.IsAdmin {
			return errors.New("local recovery credential does not resolve to the expected administrator")
		}
		return nil
	}
	exists, err := userExists(baseURL, input.Username)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("local recovery account exists but its managed credential does not match")
	}

	output, err := runForgejoCommand(
		"admin", "user", "create", "--admin", "--username", input.Username, "--email", input.Email,
		"--random-password", "--random-password-length", "48", "--must-change-password=false",
	)
	if err != nil {
		return errors.New("create Forgejo local recovery account failed")
	}
	match := randomPasswordPattern.FindSubmatch(output)
	if len(match) != 2 {
		cleanupLocalAdmin(input.Username)
		return errors.New("Forgejo did not return the generated bootstrap credential")
	}
	bootstrapPassword := string(match[1])
	if err := updateLocalAdminPassword(baseURL, input.Username, bootstrapPassword, input.Password); err != nil {
		cleanupLocalAdmin(input.Username)
		return err
	}
	user, authenticated, err = authenticate(baseURL, input.Username, input.Password)
	if err != nil || !authenticated || user.Login != input.Username || !user.IsAdmin {
		cleanupLocalAdmin(input.Username)
		return errors.New("Forgejo rejected the managed local recovery credential")
	}
	return nil
}

func authenticate(baseURL, username, password string) (apiUser, bool, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/user", nil)
	if err != nil {
		return apiUser{}, false, err
	}
	request.SetBasicAuth(username, password)
	response, err := httpClient.Do(request)
	if err != nil {
		return apiUser{}, false, fmt.Errorf("verify Forgejo local recovery credential: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return apiUser{}, false, nil
	}
	if response.StatusCode != http.StatusOK {
		return apiUser{}, false, fmt.Errorf("verify Forgejo local recovery credential: status %d", response.StatusCode)
	}
	var user apiUser
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&user); err != nil {
		return apiUser{}, false, fmt.Errorf("decode Forgejo current user: %w", err)
	}
	return user, true, nil
}

func userExists(baseURL, username string) (bool, error) {
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/users/"+url.PathEscape(username), nil)
	if err != nil {
		return false, err
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return false, fmt.Errorf("inspect Forgejo local recovery account: %w", err)
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("inspect Forgejo local recovery account: status %d", response.StatusCode)
	}
}

func updateLocalAdminPassword(baseURL, username, current, desired string) error {
	body, err := json.Marshal(map[string]any{"password": desired, "must_change_password": false, "active": true, "admin": true})
	if err != nil {
		return err
	}
	request, err := http.NewRequest(http.MethodPatch, baseURL+"/api/v1/admin/users/"+url.PathEscape(username), bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(username, current)
	response, err := httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("set Forgejo local recovery credential: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("set Forgejo local recovery credential: status %d", response.StatusCode)
	}
	return nil
}

func cleanupLocalAdmin(username string) {
	_, _ = runForgejoCommand("admin", "user", "delete", "--username", username)
}

func ensureOIDC(input oidcInput) error {
	if input.Name == "" || input.ClientID == "" || input.ClientSecret == "" || input.DiscoveryURL == "" || input.AdminGroup == "" {
		return errors.New("OIDC reconciliation input is incomplete")
	}
	list, err := runForgejoCommand("admin", "auth", "list")
	if err != nil {
		return errors.New("list Forgejo authentication sources failed")
	}
	id, found := authSourceID(list, input.Name)
	command := "add-oauth"
	args := []string{"admin", "auth"}
	if found {
		command = "update-oauth"
	}
	args = append(args, command)
	if found {
		args = append(args, "--id", strconv.Itoa(id))
	}
	args = append(args,
		"--name", input.Name,
		"--provider", "openidConnect",
		"--key", input.ClientID,
		"--secret", input.ClientSecret,
		"--auto-discover-url", input.DiscoveryURL,
		"--skip-local-2fa",
		"--scopes", "openid", "--scopes", "profile", "--scopes", "email", "--scopes", "groups",
		"--group-claim-name", "groups",
		"--admin-group", input.AdminGroup,
	)
	if _, err := runForgejoCommand(args...); err != nil {
		return errors.New("reconcile Forgejo OIDC source failed")
	}
	verified, err := runForgejoCommand("admin", "auth", "list")
	if err != nil {
		return errors.New("verify Forgejo OIDC source failed")
	}
	_, sourceType, enabled, ok := authSource(verified, input.Name)
	if !ok || !strings.Contains(strings.ToLower(sourceType), "oauth") || !enabled {
		return errors.New("Forgejo OIDC source is missing, disabled, or has the wrong type after reconciliation")
	}
	return nil
}

func authSourceID(output []byte, name string) (int, bool) {
	id, _, _, ok := authSource(output, name)
	return id, ok
}

func authSource(output []byte, name string) (int, string, bool, bool) {
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(strings.ReplaceAll(line, "|", " "))
		if len(fields) < 4 || fields[1] != name {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err == nil && id > 0 {
			enabled, parseErr := strconv.ParseBool(fields[len(fields)-1])
			if parseErr == nil {
				return id, strings.Join(fields[2:len(fields)-1], " "), enabled, true
			}
		}
	}
	return 0, "", false, false
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "anas-forgejo: %v\n", err)
	os.Exit(1)
}
