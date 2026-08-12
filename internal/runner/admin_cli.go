package runner

import (
	"flag"
	"fmt"
	"path/filepath"
	"strings"
)

func runAdmin(args []string, jsonMode bool) error {
	if len(args) < 2 || args[0] != "local" {
		return usageErrorf("usage: anas admin local list | credential CASK [ACCOUNT] [-w WORKSPACE]")
	}
	action := args[1]
	fs := flag.NewFlagSet("admin local "+action, flag.ContinueOnError)
	workspaceFlag := fs.String("w", "", "workspace path")
	fs.StringVar(workspaceFlag, "workspace", "", "workspace path")
	passwordOnly := fs.Bool("password-only", false, "print only the password")
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
		if len(positional) != 0 || *passwordOnly {
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
			fmt.Printf("%s\t%s\t%s\t%s\n", record.Cask, record.ID, record.Purpose, record.Username)
		}
		return nil
	case "credential":
		if len(positional) < 1 || len(positional) > 2 {
			return usageErrorf("usage: anas admin local credential CASK [ACCOUNT] [-w WORKSPACE] [--password-only] [--json]")
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
			return preconditionErrorf("secret_missing", "local administrator %s.%s has no generated password", record.Cask, record.ID)
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
	default:
		return usageErrorf("usage: anas admin local list | credential CASK [ACCOUNT] [-w WORKSPACE]")
	}
}

func valueAt(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return values[index]
}

func selectLocalAdmin(records []localAdminRecord, cask, id string) (localAdminRecord, error) {
	cask = strings.TrimSpace(cask)
	id = strings.TrimSpace(id)
	matches := []localAdminRecord{}
	for _, record := range records {
		if record.Cask == cask && (id == "" || record.ID == id) {
			matches = append(matches, record)
		}
	}
	if len(matches) == 0 {
		return localAdminRecord{}, fmt.Errorf("no local administrator for cask %q", cask)
	}
	if len(matches) > 1 {
		return localAdminRecord{}, fmt.Errorf("cask %q has multiple local administrators; specify one of their account ids", cask)
	}
	return matches[0], nil
}

func localAdminDocument(base string, record localAdminRecord, reveal bool, password string) map[string]any {
	doc := map[string]any{
		"cask": record.Cask, "id": record.ID, "purpose": record.Purpose,
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
	env, err := parseEnvFile(filepath.Join(base, "deployments", active.ActiveDeployment, "casks", record.Cask, ".env"))
	if err != nil {
		return ""
	}
	return env[record.URIFrom]
}
