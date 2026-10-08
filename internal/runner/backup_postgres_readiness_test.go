package runner

import (
	"os"
	"strings"
	"testing"
)

func TestBackupCompensationQualifiesRetainedPostgresBeforeConsumerStart(t *testing.T) {
	for _, failure := range []string{"", "start", "readiness"} {
		t.Run(failure, func(t *testing.T) {
			a, modules, log := stopBarrierFixture(t, false)
			provider := a.reg["db"]
			provider.ContractProviders = []ContractProvider{{Name: "relational_database", Interface: "postgres"}}
			provider.Hook.Phases = []string{"after_start"}
			a.reg["db"] = provider
			// No current requests: the declared Provider still owns retained DBs.
			a.resourceRequests = nil
			a.postgresMaintenance = map[string]bool{"db": true}
			body := "#!/bin/sh\ninput=$(cat)\ncase \"$input\" in *'\"phase\":\"after_start\"'*) ;; *) exit 13 ;; esac\n" +
				"case \"$input\" in *ANAS_POSTGRES_EXTENSION_MAINTENANCE*) exit 14 ;; esac\n" +
				"printf 'ready:db\\n' >> '" + log + "'\n"
			if failure == "readiness" {
				body += "exit 17\n"
			} else {
				body += "printf '{}'\n"
			}
			if err := os.WriteFile(provider.Hook.Command[0], []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			compose := a.compose.Bin[0]
			body = "#!/bin/sh\nprintf 'start:%s\\n' \"${PWD##*/}\" >> '" + log + "'\n"
			if failure == "start" {
				body += "case \"$PWD\" in */db) exit 19 ;; esac\n"
			}
			if err := os.WriteFile(compose, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			txn := &containerTransaction{ID: "backup-pause", Kind: containerTransactionKind, Modules: a.order, State: containerTransactionStopped}
			if err := writeContainerTransaction(a.base, txn); err != nil {
				t.Fatal(err)
			}
			err := finishContainerTransaction(a.base, a, modules, txn)
			calls, readErr := os.ReadFile(log)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if failure == "" {
				if err != nil || string(calls) != "start:db\nready:db\nstart:worker\n" || exists(transactionPath(a.base, txn.ID)) {
					t.Fatalf("compensation skipped PG readiness or replayed a consumer Hook: %v, %q", err, calls)
				}
			} else if err == nil || strings.Contains(string(calls), "start:worker") || !exists(transactionPath(a.base, txn.ID)) {
				t.Fatalf("failed PG barrier resumed its consumer or cleared durable retry evidence: %v, %q", err, calls)
			}
			if !a.postgresMaintenance["db"] {
				t.Fatal("compensation mutated the caller's maintenance scope")
			}
		})
	}
}
