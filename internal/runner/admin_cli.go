package runner

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/anas-project/ANAS/internal/compose"
	"github.com/anas-project/ANAS/internal/config"
)

const localAdminCandidateSecretKey = "ANAS_LOCAL_ADMIN_CANDIDATE_PASSWORD"

func runAdmin(args []string, jsonMode bool) error {
	if len(args) < 2 || args[0] != "local" {
		return usageErrorf("usage: anas admin local list | credential | rotate MODULE [ACCOUNT] [-w WORKSPACE]")
	}
	action := args[1]
	fs := flag.NewFlagSet("admin local "+action, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	passwordOnly := fs.Bool("password-only", false, "print only the password")
	prompt := fs.Bool("prompt", false, "read the new password from the terminal")
	registerJSONFlag(fs)
	positional, err := parseInterspersed(fs, args[2:])
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		return usageErrorf("%s", err.Error())
	}
	base := stateDir(workspace)
	if action == "rotate" {
		unlock, lockErr := acquireRuntimeLock(base)
		if lockErr != nil {
			return preconditionErrorf("runtime_lock_failed", "%s", lockErr.Error())
		}
		defer unlock()
	}
	state, err := loadLocalAdminState(base)
	if err != nil {
		return preconditionErrorf("local_admin_state_unreadable", "%s", err.Error())
	}
	secrets, err := loadSecretStore(base)
	if err != nil {
		return preconditionErrorf("secrets_unreadable", "%s", err.Error())
	}
	records := sortedLocalAdminRecords(state)

	switch action {
	case "list":
		if len(positional) != 0 || *passwordOnly || *prompt {
			return usageErrorf("usage: anas admin local list [-w WORKSPACE] [--json]")
		}
		docs := make([]map[string]any, 0, len(records))
		for _, record := range records {
			docs = append(docs, localAdminDocument(base, record, false, ""))
		}
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "accounts": docs})
		}
		for _, record := range records {
			fmt.Printf("%s\t%s\t%s\t%s\n", record.Module, record.ID, record.Purpose, record.Username)
		}
		return nil
	case "credential":
		if len(positional) < 1 || len(positional) > 2 {
			return usageErrorf("usage: anas admin local credential MODULE [ACCOUNT] [-w WORKSPACE] [--password-only] [--json]")
		}
		if *prompt {
			return usageErrorf("--prompt is only valid with rotate")
		}
		if *passwordOnly && jsonMode {
			return usageErrorf("--password-only and --json cannot be used together")
		}
		record, err := selectLocalAdmin(records, positional[0], valueAt(positional, 1))
		if err != nil {
			return preconditionErrorf("local_admin_missing", "%s", err.Error())
		}
		password, ok := secrets.values[record.SecretKey]
		if !ok || password == "" {
			return preconditionErrorf("secret_missing", "local administrator %s.%s has no generated password", record.Module, record.ID)
		}
		if *passwordOnly {
			fmt.Println(password)
			return nil
		}
		doc := localAdminDocument(base, record, true, password)
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "account": doc})
		}
		fmt.Printf("URL: %s\nUsername: %s\nPassword: %s\nPurpose: %s\n", doc["url"], record.Username, password, record.Purpose)
		return nil
	case "rotate":
		if len(positional) < 1 || len(positional) > 2 || *passwordOnly {
			return usageErrorf("usage: anas admin local rotate MODULE [ACCOUNT] [-w WORKSPACE] [--prompt] [--json]")
		}
		record, err := selectLocalAdmin(records, positional[0], valueAt(positional, 1))
		if err != nil {
			return preconditionErrorf("local_admin_missing", "%s", err.Error())
		}
		length := 24
		if cfg, loadErr := config.Load(filepath.Join(workspace, "config.yml")); loadErr == nil && cfg.Administration.LocalAccounts.PasswordLength >= 16 {
			length = cfg.Administration.LocalAccounts.PasswordLength
		}
		candidate, err := newLocalAdminPassword(*prompt, length)
		if err != nil {
			return confirmationErrorf("%s", err.Error())
		}
		if err := rotateLocalAdministrator(base, record, candidate); err != nil {
			return failuref("local_admin_rotate_failed", "%s", err.Error())
		}
		doc := localAdminDocument(base, record, false, "")
		if jsonMode {
			return emitOK(map[string]any{"workspace": workspace, "account": doc, "rotated": true})
		}
		fmt.Printf("%s.%s password rotated; retrieve it with: anas admin local credential %s %s -w %q\n", record.Module, record.ID, record.Module, record.ID, workspace)
		return nil
	default:
		return usageErrorf("usage: anas admin local list | credential | rotate MODULE [ACCOUNT] [-w WORKSPACE]")
	}
}

func valueAt(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return values[index]
}

func selectLocalAdmin(records []localAdminRecord, module, id string) (localAdminRecord, error) {
	module = strings.TrimSpace(module)
	id = strings.TrimSpace(id)
	matches := []localAdminRecord{}
	for _, record := range records {
		if record.Module == module && (id == "" || record.ID == id) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return localAdminRecord{}, fmt.Errorf("no local administrator for module %q", module)
	}
	if id == "" {
		for _, record := range matches {
			if record.ID == "primary" {
				return record, nil
			}
		}
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, record := range matches {
			ids = append(ids, record.ID)
		}
		return localAdminRecord{}, fmt.Errorf("module %q has multiple local administrators; ACCOUNT is a manifest account id, specify one of: %s", module, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func newLocalAdminPassword(prompt bool, length int) (string, error) {
	if !prompt {
		return randomPassword(length)
	}
	if !isTerminal(os.Stdin.Fd()) {
		return "", fmt.Errorf("--prompt requires a terminal")
	}
	read := func(label string) (string, error) {
		fmt.Fprint(os.Stderr, label)
		stty := exec.Command("stty", "-echo")
		stty.Stdin = os.Stdin
		if err := stty.Run(); err != nil {
			return "", fmt.Errorf("disable terminal echo: %w", err)
		}
		defer func() {
			restore := exec.Command("stty", "echo")
			restore.Stdin = os.Stdin
			_ = restore.Run()
			fmt.Fprintln(os.Stderr)
		}()
		value, err := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(value, "\r\n"), err
	}
	first, err := read("New password: ")
	if err != nil {
		return "", err
	}
	second, err := read("Confirm password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", fmt.Errorf("passwords do not match")
	}
	if len(first) < 16 {
		return "", fmt.Errorf("password must be at least 16 characters")
	}
	return first, nil
}

func rotateLocalAdministrator(base string, record localAdminRecord, candidate string) error {
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" {
		return fmt.Errorf("no active deployment")
	}
	a, modulesRoot, _, err := loadDeploymentApp(base, active.ActiveDeployment, compose.CLI{})
	if err != nil {
		return err
	}
	mod, ok := a.reg[record.Module]
	if !ok {
		return fmt.Errorf("module %s is not active", record.Module)
	}
	var account *LocalAccount
	for i := range mod.LocalAccounts {
		if mod.LocalAccounts[i].ID == record.ID {
			account = &mod.LocalAccounts[i]
			break
		}
	}
	if account == nil || strings.TrimSpace(account.Rotate) == "" {
		return fmt.Errorf("module %s account %s does not declare a supported rotate handler", record.Module, record.ID)
	}
	old := a.secrets.values[record.SecretKey]
	if old == "" {
		return fmt.Errorf("current secret %s is missing", record.SecretKey)
	}
	operation := localAccountOperation{Handler: account.Rotate, AccountID: record.ID, Username: record.Username, SecretKey: record.SecretKey, CandidateSecretKey: localAdminCandidateSecretKey}
	secretView := a.scopedSecrets(record.Module)
	secretView[record.SecretKey], secretView[localAdminCandidateSecretKey] = old, candidate
	dir := filepath.Join(modulesRoot, record.Module)
	env := a.moduleEnv(dir)
	if _, err := a.runLocalAccountHook(mod, "local_account_rotate", dir, env, operation, secretView); err != nil {
		return err
	}
	var projection string
	if account.ContainerFormat == "plaintext_on_bootstrap" {
		projection = localAdminPasswordFile(base, record.Module, record.ID)
		if err := writeLocalAdminPasswordFile(projection, candidate); err != nil {
			rollbackSecrets := cloneMap(secretView)
			rollbackSecrets[localAdminCandidateSecretKey] = old
			if _, rollbackErr := a.runLocalAccountHook(mod, "local_account_rollback", dir, env, operation, rollbackSecrets); rollbackErr != nil {
				return fmt.Errorf("update runtime secret projection: %v; application rollback also failed: %w", err, rollbackErr)
			}
			return fmt.Errorf("update runtime secret projection: %w", err)
		}
	}
	a.secrets.values[record.SecretKey], a.secrets.dirty = candidate, true
	if err := a.secrets.Save(); err != nil {
		var projectionErr error
		if projection != "" {
			projectionErr = writeLocalAdminPasswordFile(projection, old)
		}
		rollbackSecrets := cloneMap(secretView)
		rollbackSecrets[localAdminCandidateSecretKey] = old
		_, rollbackErr := a.runLocalAccountHook(mod, "local_account_rollback", dir, env, operation, rollbackSecrets)
		if projectionErr != nil || rollbackErr != nil {
			return fmt.Errorf("commit generated secret: %v; restore projection: %v; application rollback: %v", err, projectionErr, rollbackErr)
		}
		return fmt.Errorf("commit generated secret: %w", err)
	}
	return nil
}

func localAdminDocument(base string, record localAdminRecord, reveal bool, password string) map[string]any {
	doc := map[string]any{
		"module": record.Module, "id": record.ID, "purpose": record.Purpose,
		"username": record.Username, "url": activeLocalAdminURL(base, record),
	}
	if reveal {
		doc["password"] = password
	}
	return doc
}

func activeLocalAdminURL(base string, record localAdminRecord) string {
	active, err := loadActiveState(base)
	if err != nil || active.ActiveDeployment == "" || record.URIFrom == "" {
		return ""
	}
	env, err := parseEnvFile(filepath.Join(base, "deployments", active.ActiveDeployment, "modules", record.Module, ".env"))
	if err != nil {
		return ""
	}
	return env[record.URIFrom]
}
