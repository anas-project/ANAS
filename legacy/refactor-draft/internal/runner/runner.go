package runner

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/whlsxl/anas/internal/compose"
	"github.com/whlsxl/anas/internal/config"
)

type app struct {
	root    string
	base    string
	cfgPath string
	verbose bool
	yes     bool
	compose compose.CLI
	reg     map[string]Module
	cfg     *config.File
	env     map[string]string
	order   []string
}

func Main(args []string) error {
	if len(args) == 0 {
		args = []string{"help"}
	}
	switch args[0] {
	case "start", "build", "restart", "stop", "render", "plan":
		return run(args[0], args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Println(`anas - NAS service launcher

Usage:
  anas start   [--build] [-c config.yml] [-b ~/.anas] [--verbose]
  anas build   [-c config.yml] [-b ~/.anas] [--verbose]
  anas render  [-c config.yml] [-b ~/.anas] [--verbose]
  anas plan    [-c config.yml]
  anas restart [-b ~/.anas]
  anas stop    [-b ~/.anas]

The Go CLI reads the structured YAML format and still accepts legacy mods/envs keys.`)
}

func run(action string, args []string) error {
	fs := flag.NewFlagSet(action, flag.ContinueOnError)
	cfgPath := fs.String("c", "", "config file")
	fs.StringVar(cfgPath, "config", "", "config file")
	base := fs.String("b", "", "runtime base path")
	fs.StringVar(base, "base", "", "runtime base path")
	buildBeforeStart := fs.Bool("build", false, "build before start")
	verbose := fs.Bool("verbose", false, "debug logging")
	yes := fs.Bool("y", false, "accept defaults")
	if err := fs.Parse(args); err != nil {
		return err
	}

	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if *base == "" {
		home, _ := os.UserHomeDir()
		*base = filepath.Join(home, ".anas")
	}
	a := &app{root: root, base: *base, cfgPath: *cfgPath, verbose: *verbose, yes: *yes, reg: registry()}
	if action != "plan" {
		cli, err := compose.Detect()
		if err != nil {
			return err
		}
		a.compose = cli
	}

	actions := []string{action}
	if action == "start" && *buildBeforeStart {
		actions = []string{"build", "start"}
	}
	return a.execute(actions)
}

func (a *app) execute(actions []string) error {
	release := filepath.Join(a.base, "release")
	tmp := filepath.Join(a.base, "tmp")
	useTmp := contains(actions, "build") || (contains(actions, "start") && a.cfgPath != "")
	work := release
	if useTmp {
		work = tmp
	}
	cfgPath := a.cfgPath
	if cfgPath == "" {
		if useTmp {
			cfgPath = "config.yml"
		} else {
			cfgPath = filepath.Join(release, "config.yml")
		}
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	a.cfg = cfg
	a.env = cfg.BaseEnv()
	a.order, err = a.resolveOrder(cfg.Modules)
	if err != nil {
		return err
	}
	a.applyModuleDefaults()
	if err := a.calculate(work); err != nil {
		return err
	}

	if contains(actions, "plan") {
		fmt.Println(strings.Join(a.order, "\n"))
		return nil
	}
	if useTmp {
		if err := os.RemoveAll(tmp); err != nil {
			return err
		}
	}
	if err := a.renderAll(work); err != nil {
		return err
	}
	if contains(actions, "render") {
		return copyFile(cfgPath, filepath.Join(work, "config.yml"))
	}
	if contains(actions, "build") {
		if err := a.each(work, func(mod Module, dir string, services []string) error {
			if mod.Name == "core" {
				return nil
			}
			args := append([]string{"build"}, services...)
			return a.compose.Run(dir, "anas_"+mod.Name, a.env, args...)
		}); err != nil {
			return err
		}
	}
	if contains(actions, "start") {
		if useTmp && exists(release) {
			_ = a.stopRelease(release)
		}
		if err := a.each(work, func(mod Module, dir string, services []string) error {
			if mod.Name == "core" {
				return nil
			}
			args := append([]string{"up", "-d"}, services...)
			return a.compose.Run(dir, "anas_"+mod.Name, a.env, args...)
		}); err != nil {
			return err
		}
	}
	if contains(actions, "restart") {
		_ = a.stopRelease(release)
		if err := a.each(work, func(mod Module, dir string, services []string) error {
			if mod.Name == "core" {
				return nil
			}
			args := append([]string{"up", "-d"}, services...)
			return a.compose.Run(dir, "anas_"+mod.Name, a.env, args...)
		}); err != nil {
			return err
		}
	}
	if contains(actions, "stop") {
		return a.stopRelease(release)
	}
	if useTmp {
		if err := copyFile(cfgPath, filepath.Join(work, "config.yml")); err != nil {
			return err
		}
		if err := os.RemoveAll(release); err != nil {
			return err
		}
		if err := os.Rename(tmp, release); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) stopRelease(release string) error {
	if !exists(release) {
		return nil
	}
	for i := len(a.order) - 1; i >= 0; i-- {
		name := a.order[i]
		if name == "core" {
			continue
		}
		dir := filepath.Join(release, name)
		if !exists(dir) {
			continue
		}
		_ = a.compose.Run(dir, "anas_"+name, a.env, "down")
	}
	return nil
}

func (a *app) resolveOrder(mods []string) ([]string, error) {
	seen := map[string]bool{}
	temp := map[string]bool{}
	var out []string
	var visit func(string) error
	visit = func(name string) error {
		if seen[name] {
			return nil
		}
		if temp[name] {
			return fmt.Errorf("dependency cycle at %s", name)
		}
		mod, ok := a.reg[name]
		if !ok {
			return fmt.Errorf("unknown module %q", name)
		}
		temp[name] = true
		deps := append([]string{}, mod.Deps...)
		if name != "core" && !contains(deps, "core") {
			deps = append([]string{"core"}, deps...)
		}
		if svc, ok := a.cfg.Services[name]; ok {
			deps = append(deps, svc.DependsOn...)
		}
		for _, dep := range deps {
			if err := visit(dep); err != nil {
				return err
			}
		}
		temp[name] = false
		seen[name] = true
		out = append(out, name)
		return nil
	}
	for _, mod := range mods {
		if err := visit(mod); err != nil {
			return nil, err
		}
	}
	for _, name := range append([]string{}, out...) {
		for _, after := range a.reg[name].RunAfter {
			if seen[after] && index(out, after) > index(out, name) {
				out = moveAfter(out, name, after)
			}
		}
	}
	return out, nil
}

func (a *app) applyModuleDefaults() {
	for _, name := range a.order {
		for k, v := range a.reg[name].Defaults {
			if a.env[k] == "" {
				a.env[k] = v
			}
		}
	}
	a.env["ALL_MODS_NAME"] = strings.Join(a.order, ",")
	ldap := []string{}
	hostReq := []string{}
	hostOpt := []string{}
	for _, name := range a.order {
		mod := a.reg[name]
		if mod.UseLDAP {
			ldap = append(ldap, name)
		}
		if mod.UseHostLAN == "required" {
			hostReq = append(hostReq, name)
		}
		if mod.UseHostLAN == "optional" {
			hostOpt = append(hostOpt, name)
		}
	}
	a.env["USE_LDAP_MODS_NAME"] = strings.Join(ldap, ",")
	a.env["USE_HOST_LAN_REQUIRED_MODS_NAME"] = strings.Join(hostReq, ",")
	a.env["USE_HOST_LAN_OPTIONAL_MODS_NAME"] = strings.Join(hostOpt, ",")
}

func (a *app) calculate(work string) error {
	for _, name := range a.order {
		mod := a.reg[name]
		if err := requireKeys(a.env, mod.Required); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if mod.Calc != nil {
			if err := mod.Calc(a.env, filepath.Join(work, name)); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	domains := []string{}
	for _, name := range a.order {
		key := strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_DOMAIN"
		if a.env[key] != "" {
			domains = append(domains, "inner/"+strings.Split(a.env[key], ".")[0]+"/"+name)
		}
	}
	a.env["DOMAINS"] = strings.Join(domains, ",")
	return nil
}

func (a *app) renderAll(work string) error {
	if err := os.MkdirAll(work, 0755); err != nil {
		return err
	}
	for _, name := range a.order {
		mod := a.reg[name]
		dir := filepath.Join(work, name)
		if name != "core" {
			src := filepath.Join(a.root, "casks", "mods", name)
			if err := copyDir(src, dir); err != nil {
				return err
			}
		} else if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		env := cloneMap(a.env)
		env["MODULE_NAME"] = name
		if mod.ModuleEnv != nil {
			if err := mod.ModuleEnv(env, dir); err != nil {
				return err
			}
		}
		if err := renderERBFiles(dir, env); err != nil {
			return err
		}
		if err := writeEnv(filepath.Join(dir, ".env"), env); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) each(work string, fn func(Module, string, []string) error) error {
	for _, name := range a.order {
		mod := a.reg[name]
		dir := filepath.Join(work, name)
		if name == "core" {
			if err := fn(mod, dir, nil); err != nil {
				return err
			}
			continue
		}
		services, err := a.services(mod, dir)
		if err != nil {
			return err
		}
		if err := fn(mod, dir, services); err != nil {
			return err
		}
	}
	return nil
}

func (a *app) services(mod Module, dir string) ([]string, error) {
	out, err := a.compose.Output(dir, "anas_"+mod.Name, a.env, "config", "--services")
	if err != nil {
		return nil, err
	}
	services := fieldsLines(out)
	if mod.Services != nil {
		services = mod.Services(a.env, services)
	}
	sort.Strings(services)
	return services, nil
}

func writeEnv(path string, env map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	w := bufio.NewWriter(f)
	for _, k := range keys {
		fmt.Fprintf(w, "%s=%s\n", k, quoteEnv(env[k]))
	}
	return w.Flush()
}

func quoteEnv(v string) string {
	if v == "" {
		return "''"
	}
	if strings.ContainsAny(v, " \t\n\r'\"#$`\\") {
		return "'" + strings.ReplaceAll(v, "'", "'\"'\"'") + "'"
	}
	return v
}

var erbExpr = regexp.MustCompile(`<%=\s*(?:envs\[['"]([^'"]+)['"]\]|"#\{envs\[['"]([^'"]+)['"]\]\}")\s*%>`)
var erbIf = regexp.MustCompile(`(?s)<%\s*if\s+envs\[['"]([^'"]+)['"]\]\s*==\s*['"]([^'"]+)['"]\s*%>(.*?)<%\s*end\s*%>`)

func renderERBFiles(root string, env map[string]string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".erb") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(b)
		s = erbIf.ReplaceAllStringFunc(s, func(m string) string {
			g := erbIf.FindStringSubmatch(m)
			if len(g) == 4 && env[g[1]] == g[2] {
				return g[3]
			}
			return ""
		})
		s = erbExpr.ReplaceAllStringFunc(s, func(m string) string {
			g := erbExpr.FindStringSubmatch(m)
			key := g[1]
			if key == "" {
				key = g[2]
			}
			return env[key]
		})
		dst := strings.TrimSuffix(path, ".erb")
		if err := os.WriteFile(dst, []byte(s), 0644); err != nil {
			return err
		}
		return os.Remove(path)
	})
}

func copyDir(src, dst string) error {
	if !exists(src) {
		return fmt.Errorf("module directory not found: %s", src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return copyFileMode(path, target, info.Mode())
	})
}

func copyFile(src, dst string) error {
	return copyFileMode(src, dst, 0644)
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func cloneMap(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func fieldsLines(s string) []string {
	out := []string{}
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func contains[T comparable](items []T, item T) bool {
	for _, v := range items {
		if v == item {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func index(items []string, item string) int {
	for i, v := range items {
		if v == item {
			return i
		}
	}
	return -1
}

func moveAfter(items []string, item, after string) []string {
	out := []string{}
	for _, v := range items {
		if v != item {
			out = append(out, v)
		}
	}
	i := index(out, after)
	if i < 0 {
		return append(out, item)
	}
	out = append(out[:i+1], append([]string{item}, out[i+1:]...)...)
	return out
}
