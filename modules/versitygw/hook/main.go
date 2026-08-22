package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var supportedHookABIs = []string{"anas.module-hook/v1"}

type hookRequest struct {
	ABI     string            `json:"abi"`
	Phase   string            `json:"phase"`
	Module  string            `json:"module"`
	Workdir string            `json:"workdir"`
	Env     map[string]string `json:"env"`
	Secrets map[string]string `json:"secrets"`
}

type hookResponse struct {
	Env     map[string]string `json:"env,omitempty"`
	Secrets map[string]string `json:"secrets,omitempty"`
}

type secretStore struct {
	values map[string]string
}

func (s *secretStore) Ensure(key string, generate func() (string, error)) (string, error) {
	if value := s.values[key]; value != "" {
		return value, nil
	}
	value, err := generate()
	if err != nil {
		return "", err
	}
	s.values[key] = value
	return value, nil
}

func main() {
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fail(err)
	}
	var request hookRequest
	if err := json.Unmarshal(body, &request); err != nil {
		fail(err)
	}
	if !supportedABI(request.ABI) {
		fail(fmt.Errorf("unsupported ABI %q", request.ABI))
	}
	response, err := handle(request)
	if err != nil {
		fail(err)
	}
	if response.Env == nil {
		response.Env = map[string]string{}
	}
	if response.Secrets == nil {
		response.Secrets = map[string]string{}
	}
	out, err := json.Marshal(response)
	if err != nil {
		fail(err)
	}
	fmt.Print(string(out))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func supportedABI(value string) bool {
	for _, supported := range supportedHookABIs {
		if value == supported {
			return true
		}
	}
	return false
}

func handle(request hookRequest) (hookResponse, error) {
	if request.Phase != "calculate" {
		return hookResponse{}, nil
	}
	env := cloneMap(request.Env)
	secrets := &secretStore{values: cloneMap(request.Secrets)}
	if err := calculate(request.Module, env, request.Workdir, secrets); err != nil {
		return hookResponse{}, err
	}
	return hookResponse{
		Env:     changed(request.Env, env),
		Secrets: changed(request.Secrets, secrets.values),
	}, nil
}

func calculate(module string, env map[string]string, _ string, secrets *secretStore) error {
	if module != "versitygw" {
		return nil
	}

	if env["VERSITYGW_ROOT_SECRET_KEY"] == "" {
		secret, err := secrets.Ensure("VERSITYGW_ROOT_SECRET_KEY", randomSecret)
		if err != nil {
			return fmt.Errorf("generate VersityGW root secret: %w", err)
		}
		env["VERSITYGW_ROOT_SECRET_KEY"] = secret
	}

	env["VERSITYGW_HOSTNAME"] = env["CONTAINER_PREFIX"] + "versitygw"
	env["VERSITYGW_DOMAIN"] = env["VERSITYGW_DOMAIN_PREFIX"] + "." + env["BASE_DOMAIN"]
	env["VERSITYGW_DOMAIN_PORT"] = env["VERSITYGW_DOMAIN"] + ":" + env["TRAEFIK_BASE_PORT"]
	env["VERSITYGW_ENDPOINT"] = "https://" + env["VERSITYGW_DOMAIN_PORT"]
	env["VERSITYGW_OBJECTS_PATH"] = filepath.Join(env["DATA_PATH"], "versitygw", "objects")
	env["VERSITYGW_IAM_PATH"] = filepath.Join(env["DATA_PATH"], "versitygw", "iam")

	// Publish the provider-neutral object-storage output ABI. The runner copies
	// these values into each bound consumer's private
	// ANAS_OBJECT_STORAGE_BINDING__<MODULE>__* namespace, so consumers never
	// read VersityGW-specific environment keys.
	env["ANAS_OBJECT_STORAGE_S3_ENDPOINT"] = env["VERSITYGW_ENDPOINT"]
	env["ANAS_OBJECT_STORAGE_S3_REGION"] = env["VERSITYGW_REGION"]
	env["ANAS_OBJECT_STORAGE_S3_ACCESS_KEY_ID"] = env["VERSITYGW_ROOT_ACCESS_KEY"]
	env["ANAS_OBJECT_STORAGE_S3_SECRET_ACCESS_KEY"] = env["VERSITYGW_ROOT_SECRET_KEY"]
	env["ANAS_OBJECT_STORAGE_S3_PATH_STYLE"] = "true"
	return nil
}

func randomSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func cloneMap(input map[string]string) map[string]string {
	output := map[string]string{}
	for key, value := range input {
		output[key] = value
	}
	return output
}

func changed(before, after map[string]string) map[string]string {
	output := map[string]string{}
	for key, value := range after {
		if before[key] != value {
			output[key] = value
		}
	}
	return output
}
