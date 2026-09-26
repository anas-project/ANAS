package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const (
	actionsAccountSchema = "anas.actions-account/v1"
	actionsAccountName   = "anas_actions_controller"
	actionsAccountEmail  = "anas_actions_controller@localhost.invalid"
	actionsAccountRoot   = forgejoData + "/anas-actions-account"
)

var errActionsAccount = errors.New("Forgejo managed Actions account identity, credential or transition could not be verified")

type actionsAccountInput struct {
	Schema             string `json:"schema"`
	Enabled            *bool  `json:"enabled"`
	ControllerPassword string `json:"controller_password"`
	ManagerUsername    string `json:"manager_username"`
	ManagerPassword    string `json:"manager_password"`
}

type actionsAccountUser struct {
	ID      int64  `json:"id"`
	Login   string `json:"login"`
	Email   string `json:"email"`
	IsAdmin bool   `json:"is_admin"`
}

type actionsAccountReceipt struct {
	Schema   string `json:"schema"`
	UserID   int64  `json:"user_id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	State    string `json:"state"`
	Proof    string `json:"proof"`
}

type actionsAccountStore struct {
	root string
	uid  int
}

func (input actionsAccountInput) valid() bool {
	return input.Schema == actionsAccountSchema && input.Enabled != nil &&
		regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(input.ControllerPassword) &&
		regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`).MatchString(input.ManagerUsername) &&
		input.ManagerUsername != actionsAccountName && len(input.ManagerPassword) >= 16 && len(input.ManagerPassword) <= 256 &&
		!strings.ContainsAny(input.ManagerPassword, "\x00\r\n")
}

// All account-control inputs are fixed fields over stdin. In particular there
// is no target username, API address, path, command or password argv option.
func decodeActionsAccountInput(reader io.Reader) (actionsAccountInput, error) {
	var input actionsAccountInput
	body, err := io.ReadAll(io.LimitReader(reader, 8193))
	defer clear(body)
	if err != nil || len(body) > 8192 || !exactAccountObject(body, []string{"schema", "enabled", "controller_password", "manager_username", "manager_password"}, true) {
		return input, errActionsAccount
	}
	if json.Unmarshal(body, &input) != nil || !input.valid() {
		return actionsAccountInput{}, errActionsAccount
	}
	return input, nil
}

func exactAccountObject(body []byte, required []string, closed bool) bool {
	d := json.NewDecoder(bytes.NewReader(body))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return false
		}
		seen[key] = true
		known := false
		for _, field := range required {
			if key == field {
				known = true
			} else if strings.EqualFold(key, field) {
				return false
			}
		}
		if closed && !known {
			return false
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return false
		}
		if known && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') || d.Decode(&struct{}{}) != io.EOF {
		return false
	}
	for _, field := range required {
		if !seen[field] {
			return false
		}
	}
	return true
}

func (s actionsAccountStore) locked(fn func() error) error {
	if !filepath.IsAbs(s.root) || filepath.Clean(s.root) != s.root {
		return errActionsAccount
	}
	if err := os.Mkdir(s.root, 0700); err != nil && !os.IsExist(err) {
		return errActionsAccount
	}
	info, err := os.Lstat(s.root)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errActionsAccount
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != uint32(s.uid) {
		return errActionsAccount
	}
	fd, err := syscall.Open(filepath.Join(s.root, "lock"), syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return errActionsAccount
	}
	f := os.NewFile(uintptr(fd), "actions-account-lock")
	defer f.Close()
	if !s.safeFile(f, 0, 0) || syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return errActionsAccount
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	current, err := os.Lstat(s.root)
	if err != nil || !os.SameFile(info, current) || current.Mode() != info.Mode() {
		return errActionsAccount
	}
	return fn()
}

func (s actionsAccountStore) safeFile(f *os.File, minimum, maximum int64) bool {
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < minimum || info.Size() > maximum {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(s.uid) && st.Nlink == 1
}

func (s actionsAccountStore) load(secret string) (*actionsAccountReceipt, error) {
	fd, err := syscall.Open(filepath.Join(s.root, "account.json"), syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errActionsAccount
	}
	f := os.NewFile(uintptr(fd), "actions-account-receipt")
	defer f.Close()
	if !s.safeFile(f, 1, 4096) {
		return nil, errActionsAccount
	}
	before, _ := f.Stat()
	body, err := io.ReadAll(io.LimitReader(f, 4097))
	defer clear(body)
	after, statErr := f.Stat()
	named, nameErr := os.Lstat(filepath.Join(s.root, "account.json"))
	if err != nil || statErr != nil || nameErr != nil || len(body) > 4096 || !s.safeFile(f, 1, 4096) ||
		!os.SameFile(before, named) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return nil, errActionsAccount
	}
	if !exactAccountObject(body, []string{"schema", "user_id", "username", "email", "state", "proof"}, true) {
		return nil, errActionsAccount
	}
	var receipt actionsAccountReceipt
	if json.Unmarshal(body, &receipt) != nil || receipt.Schema != actionsAccountSchema || receipt.UserID < 1 ||
		receipt.Username != actionsAccountName || receipt.Email != actionsAccountEmail {
		return nil, errActionsAccount
	}
	switch receipt.State {
	case "enabled", "disabled", "enabling", "disabling":
	default:
		return nil, errActionsAccount
	}
	expected := receipt.signature(secret)
	if !hmac.Equal([]byte(expected), []byte(receipt.Proof)) {
		return nil, errActionsAccount
	}
	return &receipt, nil
}

func (r actionsAccountReceipt) signature(secret string) string {
	r.Proof = ""
	body, _ := json.Marshal(r)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("ANAS managed Actions account receipt\x00"))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s actionsAccountStore) save(r *actionsAccountReceipt, secret string) error {
	r.Proof = r.signature(secret)
	body, err := json.Marshal(r)
	if err != nil {
		return errActionsAccount
	}
	f, err := os.CreateTemp(s.root, ".account-")
	if err != nil {
		return errActionsAccount
	}
	name := f.Name()
	defer os.Remove(name)
	_, err = f.Write(append(body, '\n'))
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, filepath.Join(s.root, "account.json"))
	}
	if err != nil {
		return errActionsAccount
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return errActionsAccount
	}
	defer dir.Close()
	if dir.Sync() != nil {
		return errActionsAccount
	}
	return nil
}

// Read-only identity and password changes are sent only to the fixed local
// application. Responses are bounded and discarded; neither error strings nor
// durable receipts include credentials or upstream response bodies.
func actionsAccountRequest(method, origin, path, username, password string, payload []byte) (int, actionsAccountUser, error) {
	var user actionsAccountUser
	if origin != forgejoAPI {
		return 0, user, errActionsAccount
	}
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if client.Transport == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = nil
		client.Transport = tr
		defer tr.CloseIdleConnections()
	}
	req, err := http.NewRequest(method, origin+path, bytes.NewReader(payload))
	if err != nil {
		return 0, user, errActionsAccount
	}
	req.SetBasicAuth(username, password)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, user, errActionsAccount
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	defer clear(body)
	if err != nil || len(body) > 1<<20 {
		return 0, user, errActionsAccount
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
		return resp.StatusCode, user, nil
	}
	if resp.StatusCode != 200 || !exactAccountObject(body, []string{"id", "login", "email", "is_admin"}, false) || json.Unmarshal(body, &user) != nil || user.ID < 1 {
		return resp.StatusCode, user, errActionsAccount
	}
	return resp.StatusCode, user, nil
}

func sameActionsUser(user actionsAccountUser, id int64) bool {
	return user.ID == id && id > 0 && user.Login == actionsAccountName && user.Email == actionsAccountEmail && user.IsAdmin
}

func reconcileManagedActionsAccount(input actionsAccountInput, origin string, store actionsAccountStore) error {
	if !input.valid() || origin != forgejoAPI {
		return errActionsAccount
	}
	return store.locked(func() error {
		receipt, err := store.load(input.ControllerPassword)
		if err != nil {
			return err
		}
		status, manager, err := actionsAccountRequest(http.MethodGet, origin, "/api/v1/user", input.ManagerUsername, input.ManagerPassword, nil)
		if err != nil || status != 200 || manager.Login != input.ManagerUsername || !manager.IsAdmin {
			return errActionsAccount
		}
		status, user, err := actionsAccountRequest(http.MethodGet, origin, "/api/v1/users/"+actionsAccountName, input.ManagerUsername, input.ManagerPassword, nil)
		if err != nil {
			return err
		}
		if status == 404 {
			// A known account disappearing is not permission to claim the name
			// again. Unknown disabled installations create no account at all.
			if receipt != nil {
				return errActionsAccount
			}
			if !*input.Enabled {
				return nil
			}
			if ensureLocalAdmin(localAdminInput{Username: actionsAccountName, Email: actionsAccountEmail, Password: input.ControllerPassword}, origin) != nil {
				return errActionsAccount
			}
			status, user, err = actionsAccountRequest(http.MethodGet, origin, "/api/v1/users/"+actionsAccountName, input.ManagerUsername, input.ManagerPassword, nil)
		}
		if err != nil || status != 200 || !sameActionsUser(user, user.ID) || user.ID == manager.ID {
			return errActionsAccount
		}
		if receipt != nil && !sameActionsUser(user, receipt.UserID) {
			return errActionsAccount
		}
		status, authenticated, err := actionsAccountRequest(http.MethodGet, origin, "/api/v1/user", actionsAccountName, input.ControllerPassword, nil)
		if err != nil || (status == 200 && !sameActionsUser(authenticated, user.ID)) || (status != 200 && status != 401 && status != 403) {
			return errActionsAccount
		}
		usable := status == 200
		if receipt == nil {
			// Legacy adoption requires possession of the *existing* random
			// managed password, not the name, email, manager privilege or prefix.
			if !usable {
				return errActionsAccount
			}
			receipt = &actionsAccountReceipt{Schema: actionsAccountSchema, UserID: user.ID, Username: actionsAccountName, Email: actionsAccountEmail, State: "enabled"}
			if err := store.save(receipt, input.ControllerPassword); err != nil {
				return err
			}
		}
		final := "disabled"
		if *input.Enabled {
			final = "enabled"
		}
		if usable == *input.Enabled {
			receipt.State = final
			return store.save(receipt, input.ControllerPassword)
		}
		password := input.ControllerPassword
		if !*input.Enabled {
			value := make([]byte, 32)
			if _, err := rand.Read(value); err != nil {
				return errActionsAccount
			}
			password = hex.EncodeToString(value)
			clear(value)
		}
		receipt.State = "disabling"
		if *input.Enabled {
			receipt.State = "enabling"
		}
		if err := store.save(receipt, input.ControllerPassword); err != nil {
			return err
		}
		status, current, err := actionsAccountRequest(http.MethodGet, origin, "/api/v1/users/"+actionsAccountName, input.ManagerUsername, input.ManagerPassword, nil)
		if err != nil || status != 200 || !sameActionsUser(current, receipt.UserID) {
			return errActionsAccount
		}
		// Preserve role, email, enabled flags, user-created tokens and keys.
		// Disabling invalidates this managed password; it does not delete a user.
		payload, _ := json.Marshal(map[string]any{"password": password, "must_change_password": false})
		defer clear(payload)
		status, updated, err := actionsAccountRequest(http.MethodPatch, origin, "/api/v1/admin/users/"+actionsAccountName, input.ManagerUsername, input.ManagerPassword, payload)
		if err != nil || status != 200 || !sameActionsUser(updated, receipt.UserID) {
			return errActionsAccount
		}
		status, authenticated, err = actionsAccountRequest(http.MethodGet, origin, "/api/v1/user", actionsAccountName, input.ControllerPassword, nil)
		if err != nil || (*input.Enabled && (status != 200 || !sameActionsUser(authenticated, receipt.UserID))) ||
			(!*input.Enabled && status != 401 && status != 403) {
			return errActionsAccount
		}
		receipt.State = final
		return store.save(receipt, input.ControllerPassword)
	})
}
