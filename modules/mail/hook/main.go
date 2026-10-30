package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
)

type request struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}
type response struct {
	Env          map[string]string `json:"env,omitempty"`
	Secrets      map[string]string `json:"secrets,omitempty"`
	RuntimeFiles map[string]string `json:"runtime_files,omitempty"`
}

var serviceSecretKeys = []string{"MAIL_WEBMAIL_SERVICE_SECRET", "MAIL_DOVECOT_SERVICE_SECRET", "MAIL_SENDER_SERVICE_SECRET", "MAIL_DMS_SERVICE_SECRET", "MAIL_WEBMAIL_ENCRYPTION_SECRET"}

func handle(r request) (response, error) {
	if r.ABI != "anas.module-hook/v1" || r.Module != "mail" {
		return response{}, errors.New("invalid mail hook request")
	}
	if r.Phase != "calculate" && r.Phase != "validate" && r.Phase != "render_env" && r.Phase != "runtime_restore" {
		return response{}, errors.New("unsupported mail hook phase")
	}
	if err := validateServiceAPI(r.Env); err != nil {
		return response{}, err
	}
	final := r.Phase == "render_env" || r.Phase == "runtime_restore"
	if r.Phase == "validate" {
		return response{}, validateConfiguration(r.Env)
	}
	settings, err := deploymentSettings(r.Env, final)
	if err != nil {
		return response{}, err
	}
	switch r.Phase {
	case "calculate":
		result := response{Env: settings.env, Secrets: map[string]string{}}
		seen := map[string]bool{}
		for _, key := range serviceSecretKeys {
			value := r.Secrets[key]
			if value == "" {
				raw := make([]byte, 32)
				if _, err := rand.Read(raw); err != nil {
					return response{}, errors.New("mail service credential generation failed")
				}
				value = hex.EncodeToString(raw)
				result.Secrets[key] = value
			}
			if !serviceSecretPattern.MatchString(value) || seen[value] {
				return response{}, errors.New("mail service credentials must be valid and independent")
			}
			seen[value] = true
		}
		return result, nil
	default:
		if err := prepareDisabledServiceAPIDirectory(r.Env); err != nil {
			return response{}, err
		}
		files, err := settings.runtimeFiles(r.Secrets)
		if err != nil {
			return response{}, err
		}
		return response{Env: settings.env, RuntimeFiles: files}, nil
	}
}
func run(in io.Reader, out io.Writer) error {
	decoder := json.NewDecoder(io.LimitReader(in, 4<<20))
	var r request
	if decoder.Decode(&r) != nil {
		return errors.New("invalid mail hook input")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("mail hook expects one request")
	}
	result, err := handle(r)
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(result)
}
func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
