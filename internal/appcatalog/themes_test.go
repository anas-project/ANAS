// TEST_CASES: APPTHEME-T-004
package appcatalog

import (
	"bytes"
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/anas-project/ANAS/internal/apptheme"
)

func TestCustomThemeBindsPlanFreezesImagesAndKeepsOriginals(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "original.png"), 512, color.NRGBA{R: 180, A: 255})
	writePNG(t, filepath.Join(root, "theme.png"), 512, color.NRGBA{B: 180, A: 255})
	original, _ := os.ReadFile(filepath.Join(root, "original.png"))
	themed, _ := os.ReadFile(filepath.Join(root, "theme.png"))
	theme := apptheme.Theme{Schema: apptheme.Schema, ID: "custom-test", Name: map[string]string{"en": "Custom"}, Icons: map[string][]byte{"cloud": themed}}
	file := filepath.Join(root, "theme.json")
	save := func() {
		raw, e := apptheme.Canonical(theme)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(file, raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	save()
	cfg := Config{IconTheme: "custom", ThemeFile: "theme.json"}
	in := sampleInput("cloud")
	in.Root = root
	in.Icons = IconInput{Source: "original.png"}
	compile := func() (Catalog, Assets, map[string][]byte) {
		c, inputs, e := Compile(cfg, []Input{in}, root, "en")
		if e != nil {
			t.Fatal(e)
		}
		c, a, f, e := BuildAssets(c, inputs)
		if e != nil {
			t.Fatal(e)
		}
		c, a, f, e = ApplyIconTheme(cfg, root, c, a, f)
		if e != nil {
			t.Fatal(e)
		}
		return c, a, f
	}
	before, e := SourcesDigest(cfg, []Input{in}, root)
	if e != nil {
		t.Fatal(e)
	}
	c, a, files := compile()
	app := c.Applications[0]
	if c.IconTheme != "custom-test" || len(app.OriginalIcons) != 7 {
		t.Fatal("theme metadata or original set lost")
	}
	for _, size := range IconSizes {
		key := stringSize(size)
		want, _ := apptheme.Render(themed, size)
		if !bytes.Equal(files[app.Icons[key].Path], want) {
			t.Fatal("wrong theme pixels", size)
		}
		old, _ := apptheme.Render(original, size)
		if !bytes.Equal(files[app.OriginalIcons[key].Path], old) {
			t.Fatal("original pixels lost", size)
		}
	}
	bundle := filepath.Join(root, "bundle")
	if _, e = WriteBundle(bundle, c, a, files); e != nil {
		t.Fatal(e)
	}
	theme.Icons["cloud"] = original
	save()
	after, e := SourcesDigest(cfg, []Input{in}, root)
	if e != nil || before == after {
		t.Fatal("theme-only change not in plan", e)
	}
	if e = os.Remove(file); e != nil {
		t.Fatal(e)
	}
	frozen, e := Read(filepath.Join(bundle, "catalog.json"))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := ReadIcon(bundle, frozen.Applications[0].Icons["128"])
	if e != nil || !bytes.Equal(raw, files[app.Icons["128"].Path]) {
		t.Fatal("frozen icon depends on source", e)
	}
	encoded, _ := json.Marshal(frozen)
	if bytes.Contains(encoded, []byte("base64")) {
		t.Fatal("catalog embeds image payload")
	}
}

func TestThemeConfigurationAndPartialOverride(t *testing.T) {
	for _, cfg := range []Config{{IconTheme: "unknown"}, {IconTheme: "soft", ThemeFile: "file.json"}, {IconTheme: "custom"}, {ThemeFile: "file.json"}} {
		if cfg.Validate() != nil {
			continue
		}
		t.Fatalf("invalid config accepted %#v", cfg)
	}
	root := t.TempDir()
	c, ins, e := Compile(Config{}, []Input{sampleInput("cloud")}, root, "en")
	if e != nil {
		t.Fatal(e)
	}
	c, a, f, e := BuildAssets(c, ins)
	if e != nil {
		t.Fatal(e)
	}
	before := c.Applications[0].Icons["128"]
	c, _, _, e = ApplyIconTheme(Config{IconTheme: "soft"}, root, c, a, f)
	if e != nil || c.Applications[0].Icons["128"] != before || len(c.Applications[0].OriginalIcons) != 0 {
		t.Fatal("missing app icon must retain its source", e)
	}
	if e = os.WriteFile(filepath.Join(root, "reserved.json"), []byte(`{"schema":"anas.application-theme/v1","id":"soft","name":{"en":"Bad"},"icons":{}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ConfiguredTheme(Config{IconTheme: "custom", ThemeFile: "reserved.json"}, root); e == nil {
		t.Fatal("reserved ID accepted")
	}
	if e = os.Symlink(filepath.Join(root, "reserved.json"), filepath.Join(root, "link.json")); e != nil {
		t.Fatal(e)
	}
	if _, e = ConfiguredTheme(Config{IconTheme: "custom", ThemeFile: "link.json"}, root); e == nil {
		t.Fatal("symlink accepted")
	}
}
