package incusprovision

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anas-project/ANAS/internal/incushost"
)

func TestChineseAPTSourcesPreserveTrustAndCanSwitchBack(t *testing.T) {
	rows, err := incushost.Recipes()
	if err != nil {
		t.Fatal(err)
	}
	for _, recipe := range rows {
		t.Run(recipe.ID, func(t *testing.T) {
			root := t.TempDir()
			for _, speedup := range []bool{false, true, false} {
				files, err := WriteAPTConfigFiles(root, recipe, speedup)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					body, err := os.ReadFile(file.Path)
					if err != nil || !bytes.Equal(body, file.Body) {
						t.Fatal("written policy differs", err)
					}
					if strings.HasSuffix(file.Path, "anas.sources") {
						text := string(body)
						if !bytes.Contains(body, zabblySource(recipe.Codename)) || !strings.Contains(text, "Signed-By: /usr/share/keyrings/") || !strings.Contains(text, recipe.Codename+"-security") {
							t.Fatal("signatures or security suite lost")
						}
						if strings.Contains(text, "https://mirrors.aliyun.com/") != speedup {
							t.Fatal("wrong mirror policy")
						}
						if speedup {
							paths := []string{"debian", "debian-security"}
							if recipe.Distribution == "ubuntu" {
								paths = []string{"ubuntu", "ubuntu-ports"}
							}
							for _, path := range paths {
								if !strings.Contains(text, "URIs: https://mirrors.aliyun.com/"+path+"\n") {
									t.Fatalf("missing mirror %s", path)
								}
							}
						}
					}
					if strings.HasSuffix(file.Path, "anas.pref") && speedup {
						for _, expected := range []string{"Package: incus incus-base incus-client\nPin: origin \"mirrors.aliyun.com\"\nPin-Priority: -1", "Package: *\nPin: origin \"mirrors.aliyun.com\"\nPin-Priority: 990", "Pin: origin \"pkgs.zabbly.com\"\nPin-Priority: 995"} {
							if !strings.Contains(string(body), expected) {
								t.Fatalf("pin missing: %s", expected)
							}
						}
					}
				}
			}
			source := filepath.Join(root, "etc/anas/incus-apt/sources.list.d", recipe.ID, "anas.sources")
			if err := os.WriteFile(source, []byte("unapproved policy"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := WriteAPTConfigFiles(root, recipe, true); !errors.Is(err, ErrUnsafeState) {
				t.Fatal("overwrote unrecognized policy", err)
			}
		})
	}
}

func TestInstallMirrorChoiceIsBoundAndPassedToRuntime(t *testing.T) {
	for _, speedup := range []bool{false, true} {
		rt := newFakeRuntime(t)
		backend := newBackendForTest(&memoryStore{}, rt)
		request := Request{ChineseSpeedup: speedup}
		plan, err := backend.Plan(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		changed := request
		changed.ChineseSpeedup = !speedup
		if _, err := backend.Install(context.Background(), changed, bind(plan, PhaseInstall)); !errors.Is(err, ErrDrift) || len(rt.calls) != 0 {
			t.Fatal("changed source used old approval", err)
		}
		if _, err := backend.Install(context.Background(), request, bind(plan, PhaseInstall)); err != nil {
			t.Fatal(err)
		}
		if len(rt.speedupByCall) != 1 || rt.speedupByCall[0] != speedup {
			t.Fatal("package runtime lost source choice")
		}
	}
}
