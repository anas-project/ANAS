package installer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const cloudflareAPI = "https://api.cloudflare.com/client/v4/accounts/"

var tokenPattern = regexp.MustCompile(`^[\x21-\x7e]{16,512}$`)

// Cloudflare is a fixed-origin client for the narrow reservation operations.
// It never logs provider bodies, credentials or provider error messages.
type Cloudflare struct {
	token     string
	http      *http.Client
	authorize func(context.Context) error
}

// NewCloudflareWithAuthorization rechecks the caller's current service grant
// before every provider API request. The journal still records issued writes
// conservatively: an authorization failure after an intent remains unknown.
func NewCloudflareWithAuthorization(token string, authorize func(context.Context) error) (*Cloudflare, error) {
	if authorize == nil {
		return nil, ErrJournal
	}
	c, err := NewCloudflare(token)
	if err != nil {
		return nil, err
	}
	c.authorize = authorize
	return c, nil
}

func NewCloudflare(token string) (*Cloudflare, error) {
	if !tokenPattern.MatchString(token) {
		return nil, ErrJournal
	}
	return &Cloudflare{token: token, http: &http.Client{
		Timeout:       20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

type response struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Info    json.RawMessage `json:"result_info"`
	Errors  []struct {
		Code int `json:"code"`
	} `json:"errors"`
}

func (c *Cloudflare) request(ctx context.Context, method, path string, body any) (int, response, error) {
	if c.authorize != nil && c.authorize(ctx) != nil {
		return 0, response{}, ErrUnknown
	}
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return 0, response{}, ErrUnknown
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, cloudflareAPI+path, bytes.NewReader(data))
	if err != nil {
		return 0, response{}, ErrUnknown
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(request)
	if err != nil {
		return 0, response{}, ErrUnknown
	}
	defer res.Body.Close()
	limited := io.LimitReader(res.Body, 1<<20+1)
	raw, err := io.ReadAll(limited)
	if err != nil || len(raw) > 1<<20 {
		return 0, response{}, ErrUnknown
	}
	var result response
	if err := json.Unmarshal(raw, &result); err != nil {
		return res.StatusCode, response{}, ErrUnknown
	}
	return res.StatusCode, result, nil
}

func conflict(status int, r response, code int) bool {
	if status != http.StatusConflict || r.Success {
		return false
	}
	for _, item := range r.Errors {
		if item.Code == code {
			return true
		}
	}
	return false
}

func (c *Cloudflare) CreateWorker(ctx context.Context, account, name string) (string, error) {
	if !accountID.MatchString(account) || !resourceName.MatchString(name) {
		return "", ErrJournal
	}
	status, result, err := c.request(ctx, http.MethodPost, account+"/workers/workers", map[string]string{"name": name})
	if err != nil {
		return "", ErrUnknown
	}
	if conflict(status, result, 10040) {
		return "", ErrConflict
	}
	var worker struct{ ID, Name string }
	if status < 200 || status >= 300 || !result.Success || json.Unmarshal(result.Result, &worker) != nil || !accountID.MatchString(worker.ID) || worker.Name != name {
		return "", ErrUnknown
	}
	return worker.ID, nil
}

func (c *Cloudflare) WorkerMatches(ctx context.Context, account, id, name string) (bool, error) {
	if !accountID.MatchString(account) || !accountID.MatchString(id) || !resourceName.MatchString(name) {
		return false, ErrJournal
	}
	status, result, err := c.request(ctx, http.MethodGet, account+"/workers/workers/"+id, nil)
	if err != nil {
		return false, ErrUnknown
	}
	if status == http.StatusNotFound && !result.Success {
		return false, nil
	}
	var worker struct{ ID, Name string }
	if status != http.StatusOK || !result.Success || json.Unmarshal(result.Result, &worker) != nil || !accountID.MatchString(worker.ID) || worker.Name == "" {
		return false, ErrUnknown
	}
	return worker.ID == id && worker.Name == name, nil
}

type bucketMetadata struct {
	Name         string `json:"name"`
	CreationDate string `json:"creation_date"`
	Jurisdiction string `json:"jurisdiction"`
	Location     string `json:"location"`
	StorageClass string `json:"storage_class"`
}

// The provider's creation metadata is a drift check, not an immutable
// identity or permission to adopt a bucket after an unknown create response.
func (b bucketMetadata) fingerprint(name string) (string, error) {
	if b.Name != name || b.CreationDate == "" || strings.ContainsAny(b.CreationDate+b.Jurisdiction+b.Location+b.StorageClass, "\x00\r\n") {
		return "", ErrUnknown
	}
	if _, err := time.Parse(time.RFC3339Nano, b.CreationDate); err != nil {
		return "", ErrUnknown
	}
	data, err := json.Marshal([]string{b.Name, b.CreationDate, b.Jurisdiction, b.Location, b.StorageClass})
	if err != nil {
		return "", ErrUnknown
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func (c *Cloudflare) CreateBucket(ctx context.Context, account, name string) (string, error) {
	if !accountID.MatchString(account) || !resourceName.MatchString(name) {
		return "", ErrJournal
	}
	status, result, err := c.request(ctx, http.MethodPost, account+"/r2/buckets", map[string]string{"name": name})
	if err != nil {
		return "", ErrUnknown
	}
	if conflict(status, result, 10004) {
		return "", ErrConflict
	}
	var bucket bucketMetadata
	if status < 200 || status >= 300 || !result.Success || json.Unmarshal(result.Result, &bucket) != nil {
		return "", ErrUnknown
	}
	return bucket.fingerprint(name)
}

func (c *Cloudflare) BucketMatches(ctx context.Context, account, name, expected string) (bool, error) {
	if !accountID.MatchString(account) || !resourceName.MatchString(name) || !fingerprint.MatchString(expected) {
		return false, ErrJournal
	}
	status, result, err := c.request(ctx, http.MethodGet, account+"/r2/buckets/"+name, nil)
	if err != nil {
		return false, ErrUnknown
	}
	if status == http.StatusNotFound && !result.Success {
		return false, nil
	}
	var bucket bucketMetadata
	if status != http.StatusOK || !result.Success || json.Unmarshal(result.Result, &bucket) != nil {
		return false, ErrUnknown
	}
	actual, err := bucket.fingerprint(name)
	if err != nil {
		return false, err
	}
	return actual == expected, nil
}

var _ Client = (*Cloudflare)(nil)
