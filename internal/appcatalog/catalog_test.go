package appcatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func sampleInput(name string) Input {
	return Input{Builtin: true, Application: Application{
		Metadata: Metadata{Name: name, DisplayName: Text{"en": "Cloud", "zh-CN": "云盘"}, URL: "https://cloud.example/a?q=x#part", Summary: Text{"en": "Files", "zh-CN": "文件"}, Description: Text{"en": "Shared files", "zh-CN": "共享文件"}, DocumentationURL: Text{"en": "https://docs.example/", "zh-CN": "https://docs.example/zh/"}, Categories: []string{"files", "collaboration"}, Kind: "user"},
		Enabled:  true, Access: Access{Mode: "iam", Client: name}, Visibility: Visibility{Mode: "access"}, Groups: []string{"APP_all"}, Source: Source{Kind: "module", Module: name, Path: "module.yml"},
	}}
}

func TestLanguagePrecedenceAndFallback(t *testing.T) {
	for _, tc := range []struct{ selected, browser, deployment, want string }{
		{"ru", "zh-CN", "zh-CN", "en"}, {"", "pt-BR,zh-CN;q=0.9", "zh-CN", "en"},
		{"", "zh-SG, en;q=0.8", "en", "zh-CN"}, {"zh-Hant", "en", "zh-CN", "zh-TW"},
		// TEST_CASES: PORTALUX-T-001
		{"zh-HK", "", "", "zh-TW"}, {"", "zh-MO", "", "zh-TW"}, {"zh-Hans-TW", "", "", "zh-TW"}, {"zh", "", "", "zh-CN"},
		{"fr-CA", "en", "en", "fr"}, {"", "ja,zh-CN;q=0.9", "zh-CN", "ja"}, {"es-MX", "", "", "es"},
		{"de-AT", "", "", "de"}, {"ko-KR", "", "", "ko"}, {"it", "ja", "ja", "en"},
		{"", "", "zh-CN", "zh-CN"}, {"", "*;q=1, en;q=0", "zh-CN", "zh-CN"},
		{"en-GB", "zh-CN", "zh-CN", "en"}, {"", "en;q=0.2,zh-CN;q=0.8", "en", "zh-CN"},
	} {
		if got := SelectLanguage(tc.selected, tc.browser, tc.deployment); got != tc.want {
			t.Errorf("%+v: %s", tc, got)
		}
	}
	if got := (Text{"zh-CN": "中文"}).Resolve("en"); got != "中文" {
		t.Fatal(got)
	}
	both := Text{"zh-CN": "简体", "en": "English"}
	if both.Resolve("zh-TW") != "简体" || both.Resolve("ja") != "English" || (Text{"zh-CN": "简体"}).Resolve("fr") != "简体" || (Text{"en": "English", "ja": "日本語"}).Resolve("ja") != "日本語" {
		t.Fatal("new language fallback order changed")
	}
}

func TestOptionalLanguageKeys(t *testing.T) {
	for _, lang := range SupportedLanguages {
		if err := validateText("x", Text{"zh-CN": "中", "en": "en", lang: "v"}, 64, true, true, false); err != nil {
			t.Fatal(lang, err)
		}
	}
	if validateText("x", Text{"zh-CN": "中", "en": "en", "ru": "v"}, 64, true, true, false) == nil {
		t.Fatal("unsupported language key accepted")
	}
	if validateText("x", Text{"en": "en", "ja": "v"}, 64, true, true, false) == nil {
		t.Fatal("built-in text without zh-CN accepted")
	}
}

func TestCompileBilingualMergePrimaryCategoryAndView(t *testing.T) {
	user := sampleInput("cloud")
	admin := sampleInput("cloud-admin")
	admin.Application.Source.Module = user.Application.Source.Module
	admin.Application.Kind = "admin"
	admin.Application.Groups = []string{"Admins"}
	cfg := Config{Overrides: map[string]Override{"cloud": {DisplayName: Text{"en": "Files"}}}}
	catalog, inputs, err := Compile(cfg, []Input{admin, user}, "", "ru")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.DefaultLanguage != "en" || !reflect.DeepEqual(catalog.Applications[0].Categories, []string{"files", "collaboration"}) {
		t.Fatal(catalog)
	}
	if inputs[1].Application.DisplayName["zh-CN"] != "云盘" {
		t.Fatal("override lost Chinese")
	}
	if catalog.Applications[0].Name != "cloud" || catalog.Applications[1].Name != "cloud-admin" || catalog.Applications[0].Source.Module != catalog.Applications[1].Source.Module {
		t.Fatal("display override changed identity or lost a second module entry")
	}
	if _, _, err := Compile(Config{}, []Input{sampleInput("cloud"), sampleInput("cloud")}, "", "en"); err == nil {
		t.Fatal("duplicate application names accepted")
	}
	encoded, _ := json.Marshal(catalog)
	var roundtrip Catalog
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	view := BuildView(roundtrip.Categories, roundtrip.Applications, "zh-CN", []string{"collaboration"})
	if len(view.Groups) != 2 || view.Groups[0].Kind != "user" || view.Groups[1].Kind != "admin" || view.Groups[0].Categories[0].ID != "files" {
		t.Fatal(view)
	}
	if view.Groups[0].Categories[0].Applications[0].URL != user.Application.URL {
		t.Fatal("URL changed")
	}
	if len(BuildView(catalog.Categories, catalog.Applications, "en", []string{"media"}).Groups) != 0 {
		t.Fatal("empty groups remained")
	}
	cfg.Overrides["cloud"] = Override{Categories: []string{"files", "files"}}
	if _, _, err := Compile(cfg, []Input{user}, "", "en"); err == nil {
		t.Fatal("duplicate categories accepted")
	}
}

func TestViewOrderingAndLanguagePreserveIdentity(t *testing.T) {
	low, high := 10, 20
	apps := []Application{sampleInput("zeta").Application, sampleInput("alpha").Application, sampleInput("first").Application, sampleInput("other").Application}
	apps[0].Weight, apps[1].Weight, apps[2].Weight, apps[3].Weight = &high, &high, &low, &high
	apps[3].Categories = []string{"collaboration", "files"}
	for i := range apps {
		apps[i].Icons = map[string]Icon{"64": {Path: "assets/shared.png", Width: 64, Height: 64}}
	}
	categories := []Category{
		{ID: "files", DisplayName: Text{"en": "Files", "zh-CN": "文件"}, Weight: &high},
		{ID: "collaboration", DisplayName: Text{"en": "Work", "zh-CN": "协作"}, Weight: &high},
	}
	before, _ := json.Marshal(apps)
	for _, lang := range []string{"zh-CN", "en"} {
		view := BuildView(categories, apps, lang, []string{"files", "collaboration"})
		if len(view.Groups) != 1 || len(view.Groups[0].Categories) != 2 || view.Groups[0].Categories[0].ID != "collaboration" {
			t.Fatal("same-weight categories did not use stable IDs", view)
		}
		cards := view.Groups[0].Categories[1].Applications
		if len(cards) != 3 || cards[0].Name != "first" || cards[1].Name != "alpha" || cards[2].Name != "zeta" {
			t.Fatal("weights, stable IDs or multi-category deduplication changed", cards)
		}
		for _, subgroup := range view.Groups[0].Categories {
			for _, card := range subgroup.Applications {
				if card.URL != apps[0].URL || !reflect.DeepEqual(card.Icons, apps[0].Icons) {
					t.Fatal("language changed URL or icons")
				}
			}
		}
	}
	after, _ := json.Marshal(apps)
	if !bytes.Equal(before, after) {
		t.Fatal("presentation changed source identity or permission facts")
	}
}

func writePNG(t *testing.T, path string, size int, shade color.NRGBA) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.SetNRGBA(x, y, shade)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSevenIconsFrozenDeterministicallyAndIconOnlyChange(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "logo.png"), 512, color.NRGBA{R: 80, A: 255})
	in := sampleInput("cloud")
	in.Root = root
	in.Icons = IconInput{Source: "logo.png"}
	compile := func() (Catalog, Assets, map[string][]byte) {
		c, ins, err := Compile(Config{}, []Input{in}, root, "en")
		if err != nil {
			t.Fatal(err)
		}
		c, a, f, err := BuildAssets(c, ins)
		if err != nil {
			t.Fatal(err)
		}
		return c, a, f
	}
	c, a, files := compile()
	again, againAssets, againFiles := compile()
	if !reflect.DeepEqual(c, again) || !reflect.DeepEqual(a, againAssets) || !reflect.DeepEqual(files, againFiles) {
		t.Fatal("non-deterministic bundle")
	}
	for _, size := range IconSizes {
		icon := c.Applications[0].Icons[stringSize(size)]
		info, err := png.DecodeConfig(bytes.NewReader(files[icon.Path]))
		if err != nil || info.Width != size || info.Height != size {
			t.Fatalf("size %d: %v", size, err)
		}
	}
	dir := filepath.Join(root, "bundle")
	digest, err := WriteBundle(dir, c, a, files)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := Read(filepath.Join(dir, "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	before, _ := SourcesDigest(Config{}, []Input{in}, root)
	writePNG(t, filepath.Join(root, "logo.png"), 512, color.NRGBA{B: 180, A: 255})
	after, _ := SourcesDigest(Config{}, []Input{in}, root)
	changed, assets, newFiles := compile()
	newDigest, err := WriteBundle(filepath.Join(root, "new-bundle"), changed, assets, newFiles)
	if err != nil {
		t.Fatal(err)
	}
	if digest == newDigest || before == after {
		t.Fatal("icon-only update did not change digest")
	}
	oldAgain, err := Read(filepath.Join(dir, "catalog.json"))
	if err != nil || !reflect.DeepEqual(frozen, oldAgain) {
		t.Fatal("old deployment changed")
	}
	if _, err := WriteBundle(dir, changed, assets, newFiles); err == nil {
		t.Fatal("overwrote frozen directory")
	}
}

func stringSize(size int) string { return fmt.Sprint(size) }

func TestAssetSourceRejectsLinksTraversalAndBadPixels(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "logo.png"), 512, color.NRGBA{A: 255})
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "logo.png"), filepath.Join(root, "logo.png")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../logo.png", "/etc/passwd", "linked/logo.png", "logo.png", "secrets/logo.png"} {
		if _, err := readAsset(root, path); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	writePNG(t, filepath.Join(root, "small.png"), 128, color.NRGBA{A: 255})
	data, err := readAsset(root, "small.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeIcon(data, 0); err == nil {
		t.Fatal("upscaled small source")
	}
	if _, err := decodeIcon(data, 64); err == nil {
		t.Fatal("accepted wrong custom size")
	}
}
