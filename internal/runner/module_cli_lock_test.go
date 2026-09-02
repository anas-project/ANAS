package runner

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/modulesource"
)

func TestResolveModuleSourceRecoversPendingConfigTransaction(t *testing.T) {
	oldGeneration := configTransactionGeneration{
		config: []byte(configApplicationTestBody), secrets: []byte("old secrets\n"), state: []byte("old state\n"),
	}
	newGeneration := configTransactionGeneration{
		config:  []byte(strings.Replace(configApplicationTestBody, "module_source: official", "module_source: official-cn", 1)),
		secrets: []byte("new secrets\n"), state: []byte("new state\n"),
	}
	workspace := newConfigTransactionWorkspace(t, oldGeneration)
	realRename := configTransactionRename
	failed := false
	withConfigTransactionRenameHook(t, func(source, target string) error {
		if target == filepath.Join(stateDir(workspace), "secrets.yml") && !failed {
			failed = true
			return errors.New("injected secret publish failure")
		}
		return realRename(source, target)
	})
	if err := commitWorkspaceConfigFiles(workspace, configTransactionTestOperationID, newGeneration.config, newGeneration.secrets, newGeneration.state); err == nil {
		t.Fatal("post-WAL publish failure was not reported")
	}
	configTransactionRename = realRename

	profile, resolvedWorkspace, err := resolveModuleSource("", workspace)
	if err != nil {
		t.Fatalf("resolve module source after pending transaction: %v", err)
	}
	if profile.Name != modulesource.OfficialCN || resolvedWorkspace != workspace {
		t.Fatalf("profile=%q workspace=%q", profile.Name, resolvedWorkspace)
	}
	assertConfigTransactionGeneration(t, readConfigTransactionGeneration(t, workspace), newGeneration)
}
