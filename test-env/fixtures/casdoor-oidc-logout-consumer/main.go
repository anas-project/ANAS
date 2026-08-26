package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const logoutEvent = "http://schemas.openid.net/event/backchannel-logout"

type config struct {
	issuer                string
	internalOrigin        string
	clientID              string
	clientSecret          string
	redirectURI           string
	backchannelURI        string
	managedRedirectURIs   []string
	managedBackchannelURI string
}

type mappedTransport struct {
	issuerHost string
	target     *url.URL
	base       http.RoundTripper
}

func (transport *mappedTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Host != transport.issuerHost {
		return transport.base.RoundTrip(request)
	}
	clone := request.Clone(request.Context())
	clone.URL = cloneURL(request.URL)
	clone.URL.Scheme = transport.target.Scheme
	clone.URL.Host = transport.target.Host
	clone.Host = transport.issuerHost
	return transport.base.RoundTrip(clone)
}

type appSession struct {
	ID     string `json:"id"`
	Sub    string `json:"sub"`
	SID    string `json:"sid"`
	Active bool   `json:"active"`
}

type savedCookie struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type loginState struct {
	Username       string        `json:"username"`
	Sub            string        `json:"sub"`
	SID            string        `json:"sid"`
	AppCookie      savedCookie   `json:"app_cookie"`
	CasdoorCookies []savedCookie `json:"casdoor_cookies"`
}

type logoutEvidence struct {
	Issuer   string   `json:"iss"`
	Subject  string   `json:"sub"`
	Audience []string `json:"aud"`
	IssuedAt int64    `json:"iat"`
	Expires  int64    `json:"exp"`
	JTI      string   `json:"jti"`
	SID      string   `json:"sid"`
	Event    bool     `json:"logout_event"`
	Raw      string   `json:"-"`
}

type fixture struct {
	config config
	client *http.Client

	mu             sync.Mutex
	pending        map[string]string
	sessions       map[string]*appSession
	seenJTI        map[string]struct{}
	lastLogout     logoutEvidence
	replayRejected int
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: casdoor-oidc-logout-consumer serve|login|probe|user-logout|configure|restore|admin-delete|evidence|replay"))
	}
	config, err := loadConfig()
	if err != nil {
		fatal(err)
	}
	switch os.Args[1] {
	case "serve":
		err = serve(config, os.Args[2:])
	case "login":
		err = login(config, os.Args[2:])
	case "probe":
		err = probe(config, os.Args[2:])
	case "user-logout":
		err = userLogout(config, os.Args[2:])
	case "configure":
		err = configureApplication(config, os.Args[2:], false)
	case "restore":
		err = configureApplication(config, os.Args[2:], true)
	case "admin-delete":
		err = adminDelete(config, os.Args[2:])
	case "evidence":
		err = evidence(config, os.Args[2:])
	case "replay":
		err = replay(config, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func loadConfig() (config, error) {
	result := config{
		issuer:                strings.TrimRight(os.Getenv("CASDOOR_FIXTURE_ISSUER"), "/"),
		internalOrigin:        strings.TrimRight(os.Getenv("CASDOOR_FIXTURE_INTERNAL_ORIGIN"), "/"),
		clientID:              os.Getenv("CASDOOR_FIXTURE_CLIENT_ID"),
		redirectURI:           os.Getenv("CASDOOR_FIXTURE_REDIRECT_URI"),
		backchannelURI:        os.Getenv("CASDOOR_FIXTURE_BACKCHANNEL_URI"),
		managedRedirectURIs:   splitNonempty(os.Getenv("CASDOOR_FIXTURE_MANAGED_REDIRECT_URIS")),
		managedBackchannelURI: os.Getenv("CASDOOR_FIXTURE_MANAGED_BACKCHANNEL_URI"),
	}
	secretFile := os.Getenv("CASDOOR_FIXTURE_CLIENT_SECRET_FILE")
	if secretFile != "" {
		secret, err := os.ReadFile(secretFile)
		if err != nil {
			return result, err
		}
		result.clientSecret = strings.TrimSpace(string(secret))
	}
	if result.issuer == "" || result.internalOrigin == "" || result.clientID == "" || result.clientSecret == "" || result.redirectURI == "" || result.backchannelURI == "" {
		return result, errors.New("fixture issuer, internal origin, client ID/secret, redirect URI and back-channel URI are required")
	}
	return result, nil
}

func (config config) httpClient(withJar bool) (*http.Client, error) {
	issuerURL, err := url.Parse(config.issuer)
	if err != nil {
		return nil, err
	}
	target, err := url.Parse(config.internalOrigin)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &mappedTransport{
			issuerHost: issuerURL.Host,
			target:     target,
			base:       http.DefaultTransport,
		},
	}
	if withJar {
		client.Jar, err = cookiejar.New(nil)
	}
	return client, err
}

func serve(config config, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	listen := flags.String("listen", "127.0.0.1:18081", "listen address")
	if err := flags.Parse(args); err != nil {
		return err
	}
	client, err := config.httpClient(false)
	if err != nil {
		return err
	}
	fixture := &fixture{
		config:   config,
		client:   client,
		pending:  map[string]string{},
		sessions: map[string]*appSession{},
		seenJTI:  map[string]struct{}{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", fixture.health)
	mux.HandleFunc("/control/prepare", fixture.prepare)
	mux.HandleFunc("/callback", fixture.callback)
	mux.HandleFunc("/session", fixture.session)
	mux.HandleFunc("/backchannel", fixture.backchannel)
	mux.HandleFunc("/control/evidence", fixture.logoutEvidence)
	mux.HandleFunc("/control/replay", fixture.replayLogout)
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return server.ListenAndServe()
}

func (fixture *fixture) health(writer http.ResponseWriter, _ *http.Request) {
	writer.WriteHeader(http.StatusNoContent)
}

func (fixture *fixture) prepare(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		State string `json:"state"`
		Nonce string `json:"nonce"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || input.State == "" || input.Nonce == "" {
		http.Error(writer, "invalid state/nonce", http.StatusBadRequest)
		return
	}
	fixture.mu.Lock()
	fixture.pending[input.State] = input.Nonce
	fixture.mu.Unlock()
	writer.WriteHeader(http.StatusNoContent)
}

func (fixture *fixture) callback(writer http.ResponseWriter, request *http.Request) {
	state, code := request.URL.Query().Get("state"), request.URL.Query().Get("code")
	fixture.mu.Lock()
	nonce, found := fixture.pending[state]
	delete(fixture.pending, state)
	fixture.mu.Unlock()
	if !found || code == "" {
		http.Error(writer, "invalid callback state/code", http.StatusBadRequest)
		return
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {fixture.config.clientID},
		"client_secret": {fixture.config.clientSecret},
		"code":          {code},
		"redirect_uri":  {fixture.config.redirectURI},
	}
	response, err := fixture.client.PostForm(fixture.config.issuer+"/api/login/oauth/access_token", form)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	var tokenResponse struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if json.NewDecoder(response.Body).Decode(&tokenResponse) != nil || response.StatusCode != http.StatusOK || tokenResponse.Error != "" || tokenResponse.IDToken == "" {
		http.Error(writer, "authorization code exchange failed", http.StatusBadGateway)
		return
	}
	claims, err := verifyJWT(fixture.client, fixture.config, tokenResponse.IDToken)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusUnauthorized)
		return
	}
	if claimString(claims, "nonce") != nonce || claimString(claims, "sid") == "" || claimString(claims, "sub") == "" {
		http.Error(writer, "ID token nonce/sid/sub invalid", http.StatusUnauthorized)
		return
	}
	sessionID, err := randomID()
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	session := &appSession{ID: sessionID, Sub: claimString(claims, "sub"), SID: claimString(claims, "sid"), Active: true}
	fixture.mu.Lock()
	fixture.sessions[sessionID] = session
	fixture.mu.Unlock()
	http.SetCookie(writer, &http.Cookie{Name: "casdoor_fixture_session", Value: sessionID, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
	writeJSON(writer, http.StatusOK, session)
}

func (fixture *fixture) session(writer http.ResponseWriter, request *http.Request) {
	cookie, err := request.Cookie("casdoor_fixture_session")
	if err != nil {
		http.Error(writer, "missing session", http.StatusUnauthorized)
		return
	}
	fixture.mu.Lock()
	session := fixture.sessions[cookie.Value]
	active := session != nil && session.Active
	fixture.mu.Unlock()
	if !active {
		http.Error(writer, "revoked session", http.StatusUnauthorized)
		return
	}
	writeJSON(writer, http.StatusOK, session)
}

func (fixture *fixture) backchannel(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.ParseForm() != nil {
		fmt.Fprintln(os.Stderr, "backchannel rejected: invalid request")
		http.Error(writer, "invalid request", http.StatusBadRequest)
		return
	}
	token := request.PostForm.Get("logout_token")
	claims, err := verifyJWT(fixture.client, fixture.config, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backchannel rejected: verify JWT: %v\n", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	evidence, err := validateLogoutClaims(claims, token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backchannel rejected: validate claims: %v\n", err)
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if _, replayed := fixture.seenJTI[evidence.JTI]; replayed {
		fixture.replayRejected++
		fmt.Fprintf(os.Stderr, "backchannel rejected: replay jti=%s\n", evidence.JTI)
		http.Error(writer, "logout token replay", http.StatusBadRequest)
		return
	}
	fixture.seenJTI[evidence.JTI] = struct{}{}
	for _, session := range fixture.sessions {
		if (evidence.SID != "" && session.SID == evidence.SID) || (evidence.SID == "" && session.Sub == evidence.Subject) {
			session.Active = false
		}
	}
	fixture.lastLogout = evidence
	fmt.Fprintf(os.Stderr, "backchannel accepted: sid=%s sub=%s jti=%s\n", evidence.SID, evidence.Subject, evidence.JTI)
	writer.WriteHeader(http.StatusNoContent)
}

func (fixture *fixture) logoutEvidence(writer http.ResponseWriter, _ *http.Request) {
	fixture.mu.Lock()
	evidence := fixture.lastLogout
	replayRejected := fixture.replayRejected
	fixture.mu.Unlock()
	writeJSON(writer, http.StatusOK, struct {
		logoutEvidence
		ReplayRejected int `json:"replay_rejected"`
	}{logoutEvidence: evidence, ReplayRejected: replayRejected})
}

func (fixture *fixture) replayLogout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	fixture.mu.Lock()
	raw := fixture.lastLogout.Raw
	before := fixture.replayRejected
	fixture.mu.Unlock()
	if raw == "" {
		http.Error(writer, "no logout token captured", http.StatusConflict)
		return
	}
	response, err := http.PostForm(fixture.config.backchannelURI, url.Values{"logout_token": {raw}})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadGateway)
		return
	}
	response.Body.Close()
	fixture.mu.Lock()
	after := fixture.replayRejected
	fixture.mu.Unlock()
	if response.StatusCode < 400 || after != before+1 {
		http.Error(writer, "logout replay was not rejected", http.StatusInternalServerError)
		return
	}
	fixture.mu.Lock()
	jti := fixture.lastLogout.JTI
	fixture.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]interface{}{"jti": jti, "replay_rejected": true})
}

func login(config config, args []string) error {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	username := flags.String("username", "", "Casdoor username")
	passwordFile := flags.String("password-file", "", "password file")
	stateFile := flags.String("state-file", "", "output login state")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *username == "" || *passwordFile == "" || *stateFile == "" {
		return errors.New("username, password-file and state-file are required")
	}
	passwordBytes, err := os.ReadFile(*passwordFile)
	if err != nil {
		return err
	}
	client, err := config.httpClient(true)
	if err != nil {
		return err
	}
	state, err := randomID()
	if err != nil {
		return err
	}
	nonce, err := randomID()
	if err != nil {
		return err
	}
	if err := postJSON(http.DefaultClient, config.redirectURI[:strings.LastIndex(config.redirectURI, "/")]+"/control/prepare", map[string]string{"state": state, "nonce": nonce}, nil); err != nil {
		return err
	}
	loginURL, _ := url.Parse(config.issuer + "/api/login")
	query := loginURL.Query()
	query.Set("clientId", config.clientID)
	query.Set("responseType", "code")
	query.Set("redirectUri", config.redirectURI)
	query.Set("scope", "openid email profile")
	query.Set("state", state)
	query.Set("nonce", nonce)
	loginURL.RawQuery = query.Encode()
	payload := map[string]interface{}{
		"application": "app-anas-nextcloud", "organization": "anas", "username": *username,
		"password": strings.TrimSpace(string(passwordBytes)), "autoSignin": false, "type": "code", "signinMethod": "Password",
	}
	var loginResponse struct {
		Status string `json:"status"`
		Data   string `json:"data"`
		Msg    string `json:"msg"`
	}
	response, err := doJSON(client, http.MethodPost, loginURL.String(), payload, &loginResponse)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || loginResponse.Status != "ok" || loginResponse.Data == "" {
		return fmt.Errorf("Casdoor login failed: status=%d response=%s/%s", response.StatusCode, loginResponse.Status, loginResponse.Msg)
	}
	issuerURL, _ := url.Parse(config.issuer)
	casdoorCookies := make([]savedCookie, 0)
	for _, cookie := range client.Jar.Cookies(issuerURL) {
		casdoorCookies = append(casdoorCookies, savedCookie{Name: cookie.Name, Value: cookie.Value})
	}
	callbackURL, _ := url.Parse(config.redirectURI)
	callbackQuery := callbackURL.Query()
	callbackQuery.Set("code", loginResponse.Data)
	callbackQuery.Set("state", state)
	callbackURL.RawQuery = callbackQuery.Encode()
	callbackResponse, err := client.Get(callbackURL.String())
	if err != nil {
		return err
	}
	defer callbackResponse.Body.Close()
	var session appSession
	if json.NewDecoder(callbackResponse.Body).Decode(&session) != nil || callbackResponse.StatusCode != http.StatusOK {
		return fmt.Errorf("fixture callback failed: status=%d", callbackResponse.StatusCode)
	}
	var appCookie savedCookie
	for _, cookie := range callbackResponse.Cookies() {
		if cookie.Name == "casdoor_fixture_session" {
			appCookie = savedCookie{Name: cookie.Name, Value: cookie.Value}
		}
	}
	if appCookie.Value == "" || len(casdoorCookies) == 0 {
		return errors.New("login did not create both application and Casdoor cookies")
	}
	saved := loginState{Username: *username, Sub: session.Sub, SID: session.SID, AppCookie: appCookie, CasdoorCookies: casdoorCookies}
	if err := writePrivateJSON(*stateFile, saved); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"username": *username, "sub": session.Sub, "sid": session.SID})
}

func probe(config config, args []string) error {
	flags := flag.NewFlagSet("probe", flag.ContinueOnError)
	stateFile := flags.String("state-file", "", "login state")
	expect := flags.String("expect", "active", "active or revoked")
	if err := flags.Parse(args); err != nil {
		return err
	}
	state, err := readLoginState(*stateFile)
	if err != nil {
		return err
	}
	request, _ := http.NewRequest(http.MethodGet, sessionOrigin(config)+"/session", nil)
	request.AddCookie(&http.Cookie{Name: state.AppCookie.Name, Value: state.AppCookie.Value})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	response.Body.Close()
	active := response.StatusCode == http.StatusOK
	if (*expect == "active") != active {
		return fmt.Errorf("session %s status=%d, expected %s", state.SID, response.StatusCode, *expect)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"username": state.Username, "sid": state.SID, "active": active})
}

func userLogout(config config, args []string) error {
	flags := flag.NewFlagSet("user-logout", flag.ContinueOnError)
	stateFile := flags.String("state-file", "", "login state")
	if err := flags.Parse(args); err != nil {
		return err
	}
	state, err := readLoginState(*stateFile)
	if err != nil {
		return err
	}
	client, err := config.httpClient(false)
	if err != nil {
		return err
	}
	request, _ := http.NewRequest(http.MethodGet, config.issuer+"/api/logout?client_id="+url.QueryEscape(config.clientID), nil)
	for _, cookie := range state.CasdoorCookies {
		request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Casdoor logout status=%d", response.StatusCode)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"username": state.Username, "sid": state.SID, "logout": "accepted"})
}

func configureApplication(config config, args []string, restore bool) error {
	flags := flag.NewFlagSet("configure", flag.ContinueOnError)
	backup := flags.String("backup", "", "application backup file")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *backup == "" {
		return errors.New("backup is required")
	}
	admin, err := adminClient(config)
	if err != nil {
		return err
	}
	var application map[string]interface{}
	if restore {
		if err := readJSONFile(*backup, &application); err != nil {
			return err
		}
		application["redirectUris"] = config.managedRedirectURIs
		application["backchannelLogoutUri"] = config.managedBackchannelURI
	} else {
		if _, err := getJSON(admin, config.issuer+"/api/get-application?id=admin%2Fapp-anas-nextcloud", &application); err != nil {
			return err
		}
		if err := writePrivateJSON(*backup, application["data"]); err != nil {
			return err
		}
		data, ok := application["data"].(map[string]interface{})
		if !ok {
			return errors.New("get-application did not return application data")
		}
		application = data
		redirects, _ := application["redirectUris"].([]interface{})
		if !interfaceSliceContains(redirects, config.redirectURI) {
			redirects = append(redirects, config.redirectURI)
		}
		application["redirectUris"] = redirects
		application["backchannelLogoutUri"] = config.backchannelURI
	}
	var result map[string]interface{}
	response, err := doJSON(admin, http.MethodPost, config.issuer+"/api/update-application?id=admin%2Fapp-anas-nextcloud", application, &result)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || result["status"] != "ok" {
		return fmt.Errorf("update application failed: %#v", result)
	}
	var verified map[string]interface{}
	if _, err := getJSON(admin, config.issuer+"/api/get-application?id=admin%2Fapp-anas-nextcloud", &verified); err != nil {
		return err
	}
	verifiedApplication, ok := verified["data"].(map[string]interface{})
	if !ok {
		return errors.New("updated application could not be read back")
	}
	expectedBackchannel := config.backchannelURI
	expectedRedirect := config.redirectURI
	if restore {
		expectedBackchannel = config.managedBackchannelURI
		expectedRedirect = ""
	}
	verifiedRedirects, _ := verifiedApplication["redirectUris"].([]interface{})
	if verifiedApplication["backchannelLogoutUri"] != expectedBackchannel || (expectedRedirect != "" && !interfaceSliceContains(verifiedRedirects, expectedRedirect)) {
		return errors.New("updated application logout/redirect fields were not persisted")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"application": "app-anas-nextcloud", "configuration": map[bool]string{false: "fixture", true: "restored"}[restore]})
}

func adminDelete(config config, args []string) error {
	flags := flag.NewFlagSet("admin-delete", flag.ContinueOnError)
	stateFile := flags.String("state-file", "", "target login state")
	if err := flags.Parse(args); err != nil {
		return err
	}
	state, err := readLoginState(*stateFile)
	if err != nil {
		return err
	}
	admin, err := adminClient(config)
	if err != nil {
		return err
	}
	var sessionsResponse struct {
		Status string `json:"status"`
		Data   []struct {
			Owner       string   `json:"owner"`
			Name        string   `json:"name"`
			Application string   `json:"application"`
			SessionID   []string `json:"sessionId"`
		} `json:"data"`
	}
	if _, err := getJSON(admin, config.issuer+"/api/get-sessions?owner=anas", &sessionsResponse); err != nil {
		return err
	}
	found := false
	for _, session := range sessionsResponse.Data {
		if session.Name == state.Username && session.Application == "app-anas-nextcloud" && stringSliceContains(session.SessionID, state.SID) {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("Casdoor session %s for %s was not found", state.SID, state.Username)
	}
	var tokensResponse struct {
		Status string `json:"status"`
		Data   []struct {
			Application  string `json:"application"`
			Organization string `json:"organization"`
			User         string `json:"user"`
			ExpiresIn    int    `json:"expiresIn"`
		} `json:"data"`
	}
	if _, err := getJSON(admin, config.issuer+"/api/get-tokens?owner=admin&organization=anas", &tokensResponse); err != nil {
		return err
	}
	activeTokens := 0
	for _, token := range tokensResponse.Data {
		if token.Application == "app-anas-nextcloud" && token.Organization == "anas" && token.User == state.Username && token.ExpiresIn > 0 {
			activeTokens++
		}
	}
	if activeTokens == 0 {
		return errors.New("target session has no active OIDC token for back-channel routing")
	}
	payload := map[string]string{"owner": "anas", "name": state.Username, "application": "app-anas-nextcloud"}
	var result map[string]interface{}
	endpoint := config.issuer + "/api/delete-session?sessionId=" + url.QueryEscape(state.SID)
	response, err := doJSON(admin, http.MethodPost, endpoint, payload, &result)
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK || result["status"] != "ok" || result["data"] != "Affected" {
		return fmt.Errorf("delete session failed: %#v", result)
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]interface{}{"username": state.Username, "sid": state.SID, "active_tokens": activeTokens, "admin_delete": "accepted"})
}

func evidence(config config, args []string) error {
	flags := flag.NewFlagSet("evidence", flag.ContinueOnError)
	stateFile := flags.String("state-file", "", "expected login state")
	if err := flags.Parse(args); err != nil {
		return err
	}
	state, err := readLoginState(*stateFile)
	if err != nil {
		return err
	}
	var output struct {
		logoutEvidence
		ReplayRejected int `json:"replay_rejected"`
	}
	if _, err := getJSON(http.DefaultClient, sessionOrigin(config)+"/control/evidence", &output); err != nil {
		return err
	}
	if output.SID != state.SID || output.Subject != state.Sub || output.Issuer != config.issuer || !stringSliceContains(output.Audience, config.clientID) || !output.Event || output.JTI == "" || output.IssuedAt <= 0 || output.Expires <= time.Now().Unix() || output.Expires-output.IssuedAt != 120 {
		return fmt.Errorf("logout evidence does not match target session: %#v", output)
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func replay(config config, _ []string) error {
	var output map[string]interface{}
	if err := postJSON(http.DefaultClient, sessionOrigin(config)+"/control/replay", map[string]interface{}{}, &output); err != nil {
		return err
	}
	if output["replay_rejected"] != true || output["jti"] == "" {
		return fmt.Errorf("unexpected replay result: %#v", output)
	}
	return json.NewEncoder(os.Stdout).Encode(output)
}

func adminClient(config config) (*http.Client, error) {
	password, err := io.ReadAll(io.LimitReader(os.Stdin, 64*1024))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(password)) == "" {
		return nil, errors.New("admin password is required on stdin")
	}
	client, err := config.httpClient(true)
	if err != nil {
		return nil, err
	}
	payload := map[string]interface{}{
		"application": "app-built-in", "organization": "built-in", "username": "admin_casdoor",
		"password": strings.TrimSpace(string(password)), "autoSignin": false, "type": "login", "signinMethod": "Password",
	}
	var result map[string]interface{}
	response, err := doJSON(client, http.MethodPost, config.issuer+"/api/login", payload, &result)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK || result["status"] != "ok" {
		return nil, fmt.Errorf("admin login failed: %#v", result)
	}
	return client, nil
}

func verifyJWT(client *http.Client, config config, token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("JWT is not compact")
	}
	var header map[string]interface{}
	if err := decodeJWTPart(parts[0], &header); err != nil {
		return nil, err
	}
	if header["alg"] != "RS256" || claimString(header, "kid") == "" {
		return nil, errors.New("JWT is not an identified RS256 token")
	}
	var discovery struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if _, err := getJSON(client, config.issuer+"/.well-known/openid-configuration", &discovery); err != nil {
		return nil, err
	}
	var jwks struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if _, err := getJSON(client, discovery.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	var key *rsa.PublicKey
	for _, candidate := range jwks.Keys {
		if candidate.KID == claimString(header, "kid") && candidate.KTY == "RSA" {
			decodedN, err := base64.RawURLEncoding.DecodeString(candidate.N)
			if err != nil {
				return nil, err
			}
			decodedE, err := base64.RawURLEncoding.DecodeString(candidate.E)
			if err != nil || len(decodedE) == 0 || len(decodedE) > 4 {
				return nil, errors.New("invalid RSA exponent")
			}
			exponent := 0
			for _, value := range decodedE {
				exponent = exponent<<8 | int(value)
			}
			key = &rsa.PublicKey{N: new(big.Int).SetBytes(decodedN), E: exponent}
		}
	}
	if key == nil {
		return nil, errors.New("JWT kid did not resolve to an RSA JWK")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature); err != nil {
		return nil, fmt.Errorf("verify JWT signature: %w", err)
	}
	var claims map[string]interface{}
	if err := decodeJWTPart(parts[1], &claims); err != nil {
		return nil, err
	}
	if claimString(claims, "iss") != config.issuer || !stringSliceContains(claimStrings(claims["aud"]), config.clientID) || claimInt64(claims, "exp") <= time.Now().Unix() {
		return nil, errors.New("JWT issuer, audience or expiry invalid")
	}
	return claims, nil
}

func validateLogoutClaims(claims map[string]interface{}, raw string) (logoutEvidence, error) {
	events, _ := claims["events"].(map[string]interface{})
	_, event := events[logoutEvent]
	evidence := logoutEvidence{
		Issuer: claimString(claims, "iss"), Subject: claimString(claims, "sub"), Audience: claimStrings(claims["aud"]),
		IssuedAt: claimInt64(claims, "iat"), Expires: claimInt64(claims, "exp"), JTI: claimString(claims, "jti"),
		SID: claimString(claims, "sid"), Event: event, Raw: raw,
	}
	now := time.Now().Unix()
	if !event || evidence.JTI == "" || evidence.IssuedAt > now+30 || evidence.IssuedAt < now-300 || evidence.Expires <= now || evidence.Expires-evidence.IssuedAt != 120 || (evidence.SID == "" && evidence.Subject == "") || claims["nonce"] != nil {
		return evidence, errors.New("logout token event, time, jti, sid/sub or nonce invalid")
	}
	return evidence, nil
}

func doJSON(client *http.Client, method, endpoint string, input interface{}, output interface{}) (*http.Response, error) {
	payload, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(context.Background(), method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if output != nil {
		if err := json.NewDecoder(response.Body).Decode(output); err != nil {
			return response, err
		}
	} else {
		_, _ = io.Copy(io.Discard, response.Body)
	}
	return response, nil
}

func postJSON(client *http.Client, endpoint string, input interface{}, output interface{}) error {
	response, err := doJSON(client, http.MethodPost, endpoint, input, output)
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("POST %s returned %d", endpoint, response.StatusCode)
	}
	return nil
}

func getJSON(client *http.Client, endpoint string, output interface{}) (*http.Response, error) {
	response, err := client.Get(endpoint)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(output); err != nil {
		return response, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response, fmt.Errorf("GET %s returned %d", endpoint, response.StatusCode)
	}
	return response, nil
}

func writeJSON(writer http.ResponseWriter, status int, value interface{}) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writePrivateJSON(path string, value interface{}) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(value)
}

func readJSONFile(path string, value interface{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(value)
}

func readLoginState(path string) (loginState, error) {
	var state loginState
	if path == "" {
		return state, errors.New("state-file is required")
	}
	err := readJSONFile(path, &state)
	return state, err
}

func decodeJWTPart(part string, value interface{}) error {
	decoded, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, value)
}

func claimString(claims map[string]interface{}, name string) string {
	value, _ := claims[name].(string)
	return value
}

func claimInt64(claims map[string]interface{}, name string) int64 {
	value, _ := claims[name].(float64)
	return int64(value)
}

func claimStrings(value interface{}) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}
	case []interface{}:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func sessionOrigin(config config) string {
	parsed, _ := url.Parse(config.redirectURI)
	return parsed.Scheme + "://" + parsed.Host
}

func randomID() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func cloneURL(input *url.URL) *url.URL {
	clone := *input
	return &clone
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func interfaceSliceContains(values []interface{}, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func splitNonempty(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
