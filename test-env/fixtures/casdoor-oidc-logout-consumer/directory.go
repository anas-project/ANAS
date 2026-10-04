package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

func revocationAuthBoundary(config config, args []string) error {
	if len(args) != 0 {
		return errors.New("revocation-auth accepts no arguments")
	}
	admin, err := adminClient(config)
	if err != nil {
		return err
	}
	ordinary, err := config.httpClient(false)
	if err != nil {
		return err
	}
	for _, identity := range []struct {
		name   string
		client *http.Client
		basic  bool
	}{{"ordinary-client", ordinary, true}, {"interactive-admin", admin, false}} {
		request, err := http.NewRequest(http.MethodPost, config.issuer+"/api/anas-directory-revocation", bytes.NewBufferString(`{"owner":"anas","action":"prepare","snapshot":{"owner":"anas"}}`))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		if identity.basic {
			request.SetBasicAuth(config.clientID, config.clientSecret)
		}
		response, err := identity.client.Do(request)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			return fmt.Errorf("%s revocation boundary returned HTTP %d", identity.name, response.StatusCode)
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]bool{"ordinary_client_denied": true, "interactive_admin_denied": true})
}

func (fixture *fixture) receiverControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	fixture.mu.Lock()
	fixture.rejectLogout = r.URL.Query().Get("fail") == "true"
	fixture.mu.Unlock()
	writeJSON(w, 200, map[string]bool{"configured": true})
}

func receiverControl(config config, args []string) error {
	flags := flag.NewFlagSet("receiver", flag.ContinueOnError)
	fail := flags.Bool("fail", false, "reject back-channel delivery")
	if err := flags.Parse(args); err != nil {
		return err
	}
	response, err := http.Post(sessionOrigin(config)+"/control/receiver?fail="+fmt.Sprint(*fail), "application/json", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return errors.New("receiver control failed")
	}
	return nil
}

func (fixture *fixture) refreshSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", 405)
		return
	}
	cookie, err := r.Cookie("casdoor_fixture_session")
	if err != nil {
		http.Error(w, "cookie missing", 401)
		return
	}
	fixture.mu.Lock()
	stored := fixture.sessions[cookie.Value]
	if stored == nil {
		fixture.mu.Unlock()
		http.Error(w, "unknown session", 401)
		return
	}
	session := *stored
	fixture.mu.Unlock()
	response, err := fixture.client.PostForm(fixture.config.issuer+"/api/login/oauth/access_token", url.Values{"grant_type": {"refresh_token"}, "client_id": {fixture.config.clientID}, "client_secret": {fixture.config.clientSecret}, "refresh_token": {session.RefreshToken}})
	if err != nil {
		http.Error(w, "refresh request failed", 502)
		return
	}
	defer response.Body.Close()
	var tokens struct {
		ID      string `json:"id_token"`
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tokens); err != nil {
		http.Error(w, "invalid refresh response", 502)
		return
	}
	if tokens.Error != "" || tokens.ID == "" {
		writeJSON(w, 200, map[string]any{"accepted": false, "error": tokens.Error})
		return
	}
	claims, err := verifyJWT(fixture.client, fixture.config, tokens.ID)
	if err != nil || claimString(claims, "sub") != session.Sub || claimString(claims, "sid") != session.SID {
		http.Error(w, "refreshed signature/sub/sid mismatch", 502)
		return
	}
	request, _ := http.NewRequest(http.MethodGet, fixture.config.issuer+"/api/userinfo", nil)
	request.Header.Set("Authorization", "Bearer "+tokens.Access)
	info, err := fixture.client.Do(request)
	if err != nil {
		http.Error(w, "userinfo request failed", 502)
		return
	}
	defer info.Body.Close()
	var userinfo map[string]interface{}
	if json.NewDecoder(info.Body).Decode(&userinfo) != nil || userinfo["sub"] != session.Sub {
		http.Error(w, "userinfo subject mismatch", 502)
		return
	}
	fixture.mu.Lock()
	stored.RefreshToken = tokens.Refresh
	fixture.mu.Unlock()
	writeJSON(w, 200, map[string]any{"accepted": true, "sub": session.Sub, "sid": session.SID, "userinfo": true})
}

func refreshProbe(config config, args []string) error {
	flags := flag.NewFlagSet("refresh", flag.ContinueOnError)
	statePath := flags.String("state-file", "", "login state")
	expected := flags.String("expect", "accepted", "accepted or rejected")
	if err := flags.Parse(args); err != nil {
		return err
	}
	state, err := readLoginState(*statePath)
	if err != nil {
		return err
	}
	request, _ := http.NewRequest(http.MethodPost, sessionOrigin(config)+"/control/refresh", nil)
	request.AddCookie(&http.Cookie{Name: state.AppCookie.Name, Value: state.AppCookie.Value})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		Accepted bool   `json:"accepted"`
		Sub      string `json:"sub"`
		SID      string `json:"sid"`
		Userinfo bool   `json:"userinfo"`
	}
	if json.NewDecoder(response.Body).Decode(&result) != nil || response.StatusCode != 200 {
		return errors.New("refresh probe failed")
	}
	if result.Accepted != (*expected == "accepted") {
		return fmt.Errorf("refresh accepted=%t, expected %s", result.Accepted, *expected)
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
