// TEST_CASES: APPTHEME-T-001
package apptheme

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func sourcePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 45, G: 75, B: 150, A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestThemeRoundTripAndEveryNativeSize(t *testing.T) {
	theme := Theme{Schema: Schema, ID: "example", Name: map[string]string{"en": "Example", "zh-CN": "示例"}, Icons: map[string][]byte{"application": sourcePNG(t, 512, 512)}, Colors: map[string]Palette{"light": {}, "dark": {}}}
	raw, err := Canonical(theme)
	if err != nil {
		t.Fatal(err)
	}
	read, err := Read(raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Canonical(read)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatalf("noncanonical roundtrip: %v", err)
	}
	for _, size := range []int{16, 32, 48, 64, 128, 256, 512} {
		body, err := Render(read.Icons["application"], size)
		if err != nil {
			t.Fatal(err)
		}
		c, err := png.DecodeConfig(bytes.NewReader(body))
		if err != nil || c.Width != size || c.Height != size {
			t.Fatalf("wrong output: %d %v", size, err)
		}
	}
	for _, size := range []int{16, 512} {
		body, _ := Render(read.Icons["application"], size)
		img, _ := png.Decode(bytes.NewReader(body))
		_, _, _, corner := img.At(0, 0).RGBA()
		_, _, _, center := img.At(size/2, size/2).RGBA()
		if corner > 65535/4 || center != 65535 || (size == 512 && corner != 0) {
			t.Fatal("rounded icon lost corner or center", size)
		}
	}
	if _, err := Render(raw, 24); err == nil {
		t.Fatal("unknown size accepted")
	}
}

func TestThemeRejectsExecutableUnknownAndOversizedInputs(t *testing.T) {
	base := Theme{Schema: Schema, ID: "example", Name: map[string]string{"en": "Example"}, Icons: map[string][]byte{}}
	for _, field := range []string{`"script":"alert(1)"`, `"css":"body{}"`, `"url":"https://external.invalid/a.png"`, `"html":"<b>x</b>"`} {
		raw, _ := json.Marshal(base)
		raw = append(raw[:len(raw)-1], []byte(","+field+"}")...)
		if _, err := Read(raw); err == nil {
			t.Fatalf("unknown field accepted: %s", field)
		}
	}
	for _, raw := range [][]byte{append(bytes.Repeat([]byte(" "), MaxBytes), '{'), []byte(`{} {}`)} {
		if _, err := Read(raw); err == nil {
			t.Fatal("oversize/trailing input accepted")
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*Theme)
	}{
		{"path", func(x *Theme) { x.ID = "../outside" }},
		{"unknown-language", func(x *Theme) { x.Name = map[string]string{"en": "Valid", "ru": "Unsupported"} }},
		{"combined-language", func(x *Theme) { x.Name = map[string]string{"en": "Valid", "en|zh-CN": "Unsupported"} }},
		{"css-color", func(x *Theme) { x.Colors = map[string]Palette{"light": {"bg": "url(https://bad.invalid/)"}} }},
		{"unknown-color", func(x *Theme) { x.Colors = map[string]Palette{"light": {"position": "#FFFFFF"}} }},
		{"contrast", func(x *Theme) { x.Colors = map[string]Palette{"light": {"text": "#FFFFFF"}} }},
		{"SVG", func(x *Theme) { x.Icons = map[string][]byte{"application": []byte(`<svg onload="alert(1)"/>`)} }},
		{"small", func(x *Theme) { x.Icons = map[string][]byte{"application": sourcePNG(t, 256, 256)} }},
		{"rectangle", func(x *Theme) { x.Icons = map[string][]byte{"application": sourcePNG(t, 512, 600)} }},
		{"application-path", func(x *Theme) { x.Icons = map[string][]byte{"../application": sourcePNG(t, 512, 512)} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			v := base
			test.mutate(&v)
			raw, _ := json.Marshal(v)
			if _, err := Read(raw); err == nil {
				t.Fatal("invalid theme accepted")
			}
		})
	}
}

func TestBuiltinsAreCompleteAndUseThePortableProtocol(t *testing.T) {
	for _, id := range []string{"original", "soft"} {
		v, err := Builtin(id)
		if err != nil || len(v.Icons) != 16 || len(v.Sources) != len(v.Icons) {
			t.Fatalf("incomplete built-in %s: %v", id, err)
		}
		raw, err := BuiltinPackage(id)
		if err != nil {
			t.Fatal(err)
		}
		imported, err := Read(raw)
		if err != nil {
			t.Fatal(err)
		}
		again, err := Canonical(imported)
		if err != nil || !bytes.Equal(raw, again) {
			t.Fatal("export cannot reimport identically", err)
		}
	}
}
