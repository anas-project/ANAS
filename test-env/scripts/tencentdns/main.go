// Command tencentdns issues one signed Tencent Cloud DNSPod API call and
// prints the raw response.
//
// It exists so the end-to-end script can read and restore real DNS records
// without implementing TC3-HMAC-SHA256 in shell: the signature chains four
// HMACs over a canonical request, and a mistake in it fails as an opaque
// authentication error rather than as anything a test could diagnose.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	host    = "dnspod.tencentcloudapi.com"
	service = "dnspod"
	version = "2021-03-23"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tencentdns <Action> <JSON payload>")
		os.Exit(2)
	}
	id, key := os.Getenv("TENCENTCLOUD_SECRET_ID"), os.Getenv("TENCENTCLOUD_SECRET_KEY")
	if id == "" || key == "" {
		fmt.Fprintln(os.Stderr, "TENCENTCLOUD_SECRET_ID and TENCENTCLOUD_SECRET_KEY are required")
		os.Exit(2)
	}
	body, err := call(id, key, os.Args[1], os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "tencentdns:", err)
		os.Exit(1)
	}
	fmt.Print(body)
	// A vendor error arrives as HTTP 200 with an Error member, so the exit
	// status has to come from the body rather than from the transport.
	if strings.Contains(body, `"Error"`) {
		os.Exit(1)
	}
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func call(secretID, secretKey, action, payload string) (string, error) {
	now := time.Now().UTC()
	timestamp := fmt.Sprintf("%d", now.Unix())
	date := now.Format("2006-01-02")

	canonicalHeaders := fmt.Sprintf("content-type:application/json; charset=utf-8\nhost:%s\nx-tc-action:%s\n",
		host, strings.ToLower(action))
	signedHeaders := "content-type;host;x-tc-action"
	canonicalRequest := strings.Join([]string{
		"POST", "/", "", canonicalHeaders, signedHeaders, sha256hex(payload),
	}, "\n")

	credentialScope := date + "/" + service + "/tc3_request"
	stringToSign := strings.Join([]string{
		"TC3-HMAC-SHA256", timestamp, credentialScope, sha256hex(canonicalRequest),
	}, "\n")

	// The key is derived rather than used directly, which is why the secret
	// itself never travels.
	secretDate := hmacSHA256([]byte("TC3"+secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	req, err := http.NewRequest("POST", "https://"+host, bytes.NewBufferString(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", fmt.Sprintf("TC3-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		secretID, credentialScope, signedHeaders, signature))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Host", host)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Timestamp", timestamp)
	req.Header.Set("X-TC-Version", version)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	return string(out), err
}
