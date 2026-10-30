package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/anas-project/ANAS/internal/appcatalog"
)

var serviceWorkspacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

func validateServiceAPIConfig(c Config) error {
	if c.ServiceAPIURL == "" && c.ServiceAPIWorkspace == "" {
		return nil
	}
	u, err := url.Parse(c.ServiceAPIURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || !serviceWorkspacePattern.MatchString(c.ServiceAPIWorkspace) || !filepath.IsAbs(c.ServiceAPITokenFile) {
		return errors.New("service API requires an HTTPS origin, workspace and absolute token file")
	}
	return nil
}

func (s *Server) credentialsEnabled() bool {
	return s.config.ServiceAPIURL != "" && s.config.ServiceAPIWorkspace != "" && s.config.ServiceAPITokenFile != ""
}

func credentialApplication(app appcatalog.Application) bool {
	return app.CredentialsAvailable && app.Enabled && !app.Hidden && app.Kind == "admin" && app.Access.Mode == "local" && app.Source.Kind == "module" && app.Source.Module != ""
}

type applicationCredential struct {
	Application string `json:"application"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	URL         string `json:"url"`
}

// Read the file for every operation: anasd replaces it atomically and no
// service credential is retained in portal configuration or browser state.
func readServiceToken(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return "", errDenied
	}
	f, err := os.Open(path)
	if err != nil {
		return "", errDenied
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 4096 {
		return "", errDenied
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	token := strings.TrimSpace(string(b))
	if err != nil || len(b) > 4096 || token == "" || strings.ContainsAny(token, " \t\r\n") {
		return "", errDenied
	}
	return token, nil
}

func (s *Server) fetchApplicationCredential(ctx context.Context, name, actor, requestID string) (applicationCredential, error) {
	var result applicationCredential
	token, err := readServiceToken(s.config.ServiceAPITokenFile)
	if err != nil {
		return result, errDenied
	}
	payload, _ := json.Marshal(struct {
		Actor     string `json:"actor"`
		RequestID string `json:"request_id"`
	}{actor, requestID})
	endpoint := strings.TrimRight(s.config.ServiceAPIURL, "/") + "/api/v1/service/workspaces/" + url.PathEscape(s.config.ServiceAPIWorkspace) + "/applications/" + url.PathEscape(name) + "/credentials"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// Keep the existing CA-verifying transport, but never forward the bearer
	// credential to a redirect, including a redirect on the same origin.
	client := *s.oidc.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return result, errDenied
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		response, err := client.Do(req)
		if err != nil {
			return result, errDenied
		}
		if response.StatusCode == http.StatusUnauthorized && attempt == 0 {
			response.Body.Close()
			next, readErr := readServiceToken(s.config.ServiceAPITokenFile)
			if readErr == nil && next != token {
				token = next
				continue
			}
			return result, errDenied
		}
		if response.StatusCode != http.StatusOK {
			response.Body.Close()
			return result, errDenied
		}
		b, readErr := io.ReadAll(io.LimitReader(response.Body, (16<<10)+1))
		response.Body.Close()
		if readErr != nil || len(b) > 16<<10 || json.Unmarshal(b, &result) != nil || result.Application != name || result.Username == "" || result.Password == "" {
			return applicationCredential{}, errDenied
		}
		return result, nil
	}
	return result, errDenied
}

func (s *Server) applicationCredentials(w http.ResponseWriter, r *http.Request) {
	v, actor, account, ok := s.current(w, r)
	if !ok {
		return
	}
	const action = "application.credentials.read"
	name := r.PathValue("name")
	if !s.csrf(r, v) {
		s.rejected(v.Subject, name, action, "platform_admin")
		failure(w, 403, "csrf_rejected")
		return
	}
	var body struct{}
	if !decode(r, &body) {
		failure(w, 400, "invalid_request")
		return
	}
	var selected appcatalog.Application
	for _, app := range s.catalog.Applications {
		if app.Name == name {
			selected = app
			break
		}
	}
	if !s.administrator(actor) || !credentialApplication(selected) || !applicationAllowed(s.catalog, selected, account, actor) {
		s.rejected(v.Subject, name, action, "platform_admin")
		failure(w, 403, "access_denied")
		return
	}
	if !s.recentSession(v) {
		s.rejected(v.Subject, name, action, "platform_admin")
		failure(w, 403, "recent_authentication_required")
		return
	}
	if !s.credentialsEnabled() {
		failure(w, 503, "credentials_unavailable")
		return
	}
	record := Record{ID: randomID(), Time: time.Now().UTC(), Phase: "intent", Actor: actor.Anchor, Target: name, Action: action, Rule: "platform_admin", ConfigDigest: s.digest}
	if err := s.audit.append(record); err != nil {
		failure(w, 503, "audit_unavailable")
		return
	}
	credential, err := s.fetchApplicationCredential(r.Context(), name, actor.Anchor, record.ID)
	record.Phase, record.Time, record.Service = "result", time.Now().UTC(), "unavailable"
	if err == nil {
		// An in-flight retrieval cannot outlive logout, role removal or recent
		// authentication. Check current facts again before releasing the secret.
		fresh, valid := s.store.get(v.ID)
		freshActor, directoryErr := s.directory.Person(v.Subject)
		freshAccount, iamErr := s.oidc.account(r.Context(), v.Authentication)
		if !valid || fresh.Anonymous || fresh.Subject != actor.Anchor || !s.recentSession(fresh) || directoryErr != nil || freshActor.Anchor != actor.Anchor || freshActor.GUID != actor.GUID || !freshActor.canAuthenticate(fresh.Authentication) || !s.administrator(freshActor) || iamErr != nil || !applicationAllowed(s.catalog, selected, freshAccount, freshActor) {
			err, record.Service = errDenied, "denied"
		} else {
			record.Service = "read"
		}
	}
	if auditErr := s.audit.append(record); auditErr != nil {
		failure(w, 503, "audit_unavailable")
		return
	}
	if err != nil {
		if record.Service == "denied" {
			failure(w, 403, "access_denied")
		} else {
			failure(w, 503, "credentials_unavailable")
		}
		return
	}
	respond(w, 200, credential)
}
