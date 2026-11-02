// Command check-release-modules gates publication on Module lifecycle dependencies.
// It reports every possible Provider, rather than guessing a deployment's choices.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/anas-project/ANAS/internal/runner"
	"gopkg.in/yaml.v3"
)

type service struct {
	Name         string `yaml:"name"`
	Version      string `yaml:"version"`
	Status       string `yaml:"status"`
	Capabilities struct {
		Provides []struct {
			Name       string   `yaml:"name"`
			Interfaces []string `yaml:"interfaces"`
		} `yaml:"provides"`
	} `yaml:"capabilities"`
	Contracts struct {
		Provides []struct {
			Name      string `yaml:"name"`
			Version   string `yaml:"version"`
			Interface string `yaml:"interface"`
		} `yaml:"provides"`
	} `yaml:"contracts"`
	Dependencies struct {
		Requires     []yaml.Node `yaml:"requires"`
		Alternatives []struct {
			Providers []string `yaml:"providers"`
		} `yaml:"requires_one"`
		Capabilities []struct {
			Name       string `yaml:"name"`
			EnabledBy  string `yaml:"enabled_by"`
			Interfaces struct {
				AnyOf []string `yaml:"any_of"`
			} `yaml:"interfaces"`
		} `yaml:"requires_capabilities"`
		Contracts []struct {
			Name       string   `yaml:"name"`
			Version    string   `yaml:"version"`
			EnabledBy  string   `yaml:"enabled_by"`
			Interfaces []string `yaml:"interfaces"`
		} `yaml:"contracts"`
	} `yaml:"dependencies"`
}
type edge struct {
	Target    string `json:"target"`
	Kind      string `json:"kind"`
	Condition string `json:"condition,omitempty"`
}
type finding struct {
	Release    string   `json:"release_module"`
	Consumer   string   `json:"consumer"`
	Dependency string   `json:"dependency"`
	Status     string   `json:"status"`
	Kind       string   `json:"kind"`
	Condition  string   `json:"condition,omitempty"`
	Path       []string `json:"path"`
}
type report struct {
	Release  []string  `json:"release_modules"`
	Excluded []string  `json:"excluded_modules"`
	Findings []finding `json:"dependencies_requiring_review"`
	Passed   bool      `json:"passed"`
}

func intersects(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
func edges(s service, all map[string]service) ([]edge, error) {
	out := []edge{}
	for _, n := range s.Dependencies.Requires {
		var name string
		condition := ""
		if n.Kind == yaml.ScalarNode {
			name = n.Value
		} else {
			var v struct {
				Name     string `yaml:"name"`
				Optional bool   `yaml:"optional"`
			}
			if err := n.Decode(&v); err != nil {
				return nil, err
			}
			name = v.Name
			if v.Optional {
				condition = "optional"
			}
		}
		if name == "" {
			return nil, fmt.Errorf("%s: empty direct dependency", s.Name)
		}
		out = append(out, edge{name, "module", condition})
	}
	for _, a := range s.Dependencies.Alternatives {
		for _, name := range a.Providers {
			out = append(out, edge{name, "alternative", "provider selection"})
		}
	}
	for _, d := range s.Dependencies.Capabilities {
		for name, p := range all {
			for _, c := range p.Capabilities.Provides {
				if c.Name == d.Name && (len(d.Interfaces.AnyOf) == 0 || intersects(d.Interfaces.AnyOf, c.Interfaces)) {
					out = append(out, edge{name, "capability:" + d.Name, d.EnabledBy})
					break
				}
			}
		}
	}
	for _, d := range s.Dependencies.Contracts {
		constraint, err := semver.NewConstraint(d.Version)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		for name, p := range all {
			for _, c := range p.Contracts.Provides {
				if c.Name != d.Name || !intersects(d.Interfaces, []string{c.Interface}) {
					continue
				}
				v, err := semver.NewVersion(c.Version)
				if err != nil {
					return nil, err
				}
				if constraint.Check(v) {
					out = append(out, edge{name, "contract:" + d.Name + "/" + c.Interface, d.EnabledBy})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Target+out[i].Kind < out[j].Target+out[j].Kind })
	return out, nil
}
func inspect(all map[string]service, roots ...string) (report, error) {
	r := report{Release: []string{}, Excluded: []string{}, Findings: []finding{}, Passed: true}
	for name, s := range all {
		if s.Status == "release" {
			r.Release = append(r.Release, name)
		} else {
			r.Excluded = append(r.Excluded, name)
		}
	}
	if len(roots) > 0 {
		r.Release = nil
		for _, name := range roots {
			s, ok := all[name]
			if !ok || s.Status != "release" {
				return r, fmt.Errorf("requested Module %s is not release", name)
			}
			r.Release = append(r.Release, name)
		}
	}
	sort.Strings(r.Release)
	sort.Strings(r.Excluded)
	for _, root := range r.Release {
		seen := map[string]bool{}
		var visit func(string, []string) error
		visit = func(name string, path []string) error {
			if seen[name] {
				return nil
			}
			seen[name] = true
			s, ok := all[name]
			if !ok {
				return fmt.Errorf("unknown dependency %s", name)
			}
			deps, err := edges(s, all)
			if err != nil {
				return err
			}
			for _, e := range deps {
				p, ok := all[e.Target]
				if !ok {
					return fmt.Errorf("%s depends on unknown Module %s", name, e.Target)
				}
				next := append(append([]string{}, path...), e.Target)
				if p.Status != "release" {
					r.Findings = append(r.Findings, finding{root, name, e.Target, p.Status, e.Kind, e.Condition, next})
					r.Passed = false
				}
				if err := visit(e.Target, next); err != nil {
					return err
				}
			}
			return nil
		}
		if err := visit(root, []string{root}); err != nil {
			return r, err
		}
	}
	return r, nil
}
func run() int {
	root := flag.String("root", ".", "repository root")
	selected := flag.String("module", "all", "release Module to review, or all")
	list := flag.Bool("list", false, "list release Module names as JSON; inventory only, not acceptance")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "flags only")
		return 1
	}
	inventory, err := runner.LoadBuiltinInventory(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	all := map[string]service{}
	for _, m := range inventory.Modules {
		b, err := os.ReadFile(filepath.Join(*root, "modules", m.Name, "module.yml"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		var s service
		if err := yaml.Unmarshal(b, &s); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		all[s.Name] = s
	}
	var roots []string
	if !*list && *selected != "" && *selected != "all" {
		roots = strings.Split(*selected, ",")
	}
	r, err := inspect(all, roots...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *list {
		if err := enc.Encode(r.Release); err != nil {
			return 1
		}
		return 0
	}
	if err := enc.Encode(r); err != nil {
		return 1
	}
	summary := "## Release Module dependency review\n\n"
	for _, f := range r.Findings {
		summary += fmt.Sprintf("- `%s`: `%s` → `%s` (%s, %s; condition: %s). Verify the paired requirement/plan and exact tested source before release.\n", f.Release, f.Consumer, f.Dependency, f.Status, f.Kind, f.Condition)
	}
	if p := os.Getenv("GITHUB_STEP_SUMMARY"); p != "" {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, err = f.WriteString(summary)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return 1
		}
	}
	if !r.Passed {
		fmt.Fprintln(os.Stderr, "publication blocked: non-release dependencies need human acceptance review; plan status alone is not evidence of release readiness")
		return 1
	}
	return 0
}
func main() { os.Exit(run()) }
