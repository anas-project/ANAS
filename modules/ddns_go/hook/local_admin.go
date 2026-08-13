package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

var restartDDNSGo = func(container string) error {
	return exec.Command("docker", "restart", container).Run()
}

func handleLocalAccount(req hookRequest) error {
	op := req.LocalAccount
	if req.Module != "ddns_go" || op == nil || op.AccountID != "primary" || (op.Handler != "apply-ddns-go-local-admin" && op.Handler != "rotate-ddns-go-local-admin") {
		return fmt.Errorf("ddns-go: unsupported local account handler")
	}
	current, candidate := req.Secrets[op.SecretKey], req.Secrets[op.CandidateSecretKey]
	if current == "" || candidate == "" {
		return fmt.Errorf("ddns-go: current or candidate local administrator secret is missing")
	}
	path := filepath.Join(req.Env["DATA_PATH"], "ddns_go", ".ddns_go_config.yaml")
	if req.Phase == "local_account_apply" {
		return verifyDDNSCredential(path, op.Username, candidate)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read ddns-go state: %w", err)
	}
	if err := writeDDNSCredential(path, before, op.Username, candidate); err != nil {
		return err
	}
	container := req.Env["CONTAINER_PREFIX"] + "ddns_go"
	if err := restartDDNSGo(container); err == nil {
		if err = verifyDDNSCredential(path, op.Username, candidate); err == nil {
			return nil
		}
	}
	if req.Phase == "local_account_rollback" {
		return err
	}
	if restoreErr := writeBytesAtomic(path, before, 0600); restoreErr != nil {
		return fmt.Errorf("ddns-go rotation failed (%v) and restoring state failed (%v)", err, restoreErr)
	}
	if restartErr := restartDDNSGo(container); restartErr != nil {
		return fmt.Errorf("ddns-go rotation failed (%v) and restarting restored state failed (%v)", err, restartErr)
	}
	return fmt.Errorf("ddns-go rotation failed; old password restored: %w", err)
}

func writeDDNSCredential(path string, source []byte, username, password string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(source, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("ddns-go state is not a mapping")
	}
	root := doc.Content[0]
	user := mapNodeValue(root, "user")
	if user == nil || user.Kind != yaml.MappingNode {
		user = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setNodeValue(root, "user", user)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	setNodeValue(user, "username", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: username})
	setNodeValue(user, "password", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: string(hash)})
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return writeBytesAtomic(path, out, 0600)
}

func verifyDDNSCredential(path, username, password string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("empty ddns-go state")
	}
	user := mapNodeValue(doc.Content[0], "user")
	if user == nil || mapNodeValue(user, "username") == nil || mapNodeValue(user, "username").Value != username {
		return fmt.Errorf("ddns-go username verification failed")
	}
	passwordNode := mapNodeValue(user, "password")
	if passwordNode == nil || bcrypt.CompareHashAndPassword([]byte(passwordNode.Value), []byte(password)) != nil {
		return fmt.Errorf("ddns-go password verification failed")
	}
	return nil
}

func mapNodeValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
func setNodeValue(node *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}
func writeBytesAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".local-admin-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := bytes.NewReader(data).WriteTo(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
