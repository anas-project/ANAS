// Package apptheme reads portable, data-only application themes shared by the
// native catalog builder and portal. It never fetches URLs or executes content.
package apptheme

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"
	"regexp"
	"strings"
	"sync"

	"golang.org/x/image/draw"
)

const Schema = "anas.application-theme/v1"
const MaxBytes = 32 << 20

var IDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
var applicationPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}$`)
var colorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

//go:embed builtin/*.json
var builtins embed.FS

type Palette map[string]string
type Attribution struct {
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	Generated bool   `json:"generated,omitempty"`
	Note      string `json:"note,omitempty"`
}
type Theme struct {
	Schema  string                 `json:"schema"`
	ID      string                 `json:"id"`
	Name    map[string]string      `json:"name"`
	Colors  map[string]Palette     `json:"colors,omitempty"`
	Icons   map[string][]byte      `json:"icons"`
	Sources map[string]Attribution `json:"sources,omitempty"`
}

// The contrast baseline matches the portal's existing light/dark tokens. Only
// color tokens can be overridden; layout, fonts, URLs and CSS are not accepted.
var Defaults = map[string]Palette{
	"light": {"bg": "#F3F4F6", "surface": "#FFFFFF", "surface-2": "#F7F8FA", "text": "#111318", "text-2": "#454A55", "text-3": "#5B6170", "primary": "#3B3FD8", "on-primary": "#FFFFFF", "accent-soft": "#EEEEFC", "accent-text": "#2A2E9E", "sidebar": "#14161B", "sidebar-text": "#C3C7D0", "sidebar-active": "#262A33", "ok-bg": "#E6F4EA", "ok": "#1C6A38", "warn-bg": "#FFF4E0", "warn": "#6E3C00", "danger-bg": "#FDECEC", "danger": "#A61B1B"},
	"dark":  {"bg": "#0F1115", "surface": "#171A20", "surface-2": "#1E222A", "text": "#ECEEF2", "text-2": "#BCC2CC", "text-3": "#A2A9B5", "primary": "#5458E6", "on-primary": "#FFFFFF", "accent-soft": "#25274A", "accent-text": "#B9BBFF", "sidebar": "#0B0D10", "sidebar-text": "#B4BAC6", "sidebar-active": "#1D2129", "ok-bg": "#123020", "ok": "#7EDB9F", "warn-bg": "#36280F", "warn": "#F6C978", "danger-bg": "#3A1618", "danger": "#FF9A9A"},
}

func allowedColor(key string) bool {
	_, ok := Defaults["light"][key]
	return ok || key == "line" || key == "line-strong" || key == "warn-line" || key == "danger-line" || key == "focus"
}

func luminance(value string) float64 {
	raw, _ := hex.DecodeString(strings.TrimPrefix(value, "#"))
	var result float64
	for i, weight := range []float64{.2126, .7152, .0722} {
		v := float64(raw[i]) / 255
		if v <= .04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+.055)/1.055, 2.4)
		}
		result += weight * v
	}
	return result
}

func contrast(a, b string) float64 {
	x, y := luminance(a), luminance(b)
	if x < y {
		x, y = y, x
	}
	return (x + .05) / (y + .05)
}

func (t Theme) Validate() error {
	if t.Schema != Schema || !IDPattern.MatchString(t.ID) || strings.TrimSpace(t.Name["en"]) == "" || len(t.Name) > 8 || len(t.Icons) > 128 || t.Icons == nil {
		return fmt.Errorf("invalid theme metadata")
	}
	for lang, text := range t.Name {
		switch lang {
		case "en", "zh-CN", "zh-TW", "ja", "ko", "fr", "de", "es":
		default:
			return fmt.Errorf("invalid theme language")
		}
		if len(text) > 160 || strings.TrimSpace(text) == "" {
			return fmt.Errorf("invalid theme name")
		}
	}
	for mode, overrides := range t.Colors {
		base, ok := Defaults[mode]
		if !ok {
			return fmt.Errorf("invalid color mode")
		}
		palette := Palette{}
		for k, v := range base {
			palette[k] = v
		}
		for k, v := range overrides {
			if !allowedColor(k) || !colorPattern.MatchString(v) {
				return fmt.Errorf("invalid theme color")
			}
			palette[k] = v
		}
		pairs := [][2]string{{"on-primary", "primary"}, {"accent-text", "accent-soft"}, {"sidebar-text", "sidebar"}, {"sidebar-text", "sidebar-active"}, {"ok", "ok-bg"}, {"warn", "warn-bg"}, {"danger", "danger-bg"}}
		for _, fg := range []string{"text", "text-2", "text-3"} {
			for _, bg := range []string{"bg", "surface", "surface-2"} {
				pairs = append(pairs, [2]string{fg, bg})
			}
		}
		for _, pair := range pairs {
			if contrast(palette[pair[0]], palette[pair[1]]) < 4.5 {
				return fmt.Errorf("theme text contrast below 4.5:1")
			}
		}
	}
	for name, raw := range t.Icons {
		if !applicationPattern.MatchString(name) {
			return fmt.Errorf("invalid theme application")
		}
		if _, err := decodePNG(raw); err != nil {
			return err
		}
	}
	for name, source := range t.Sources {
		if _, ok := t.Icons[name]; !ok || len(source.URL) > 2048 || len(source.Note) > 4096 || len(source.SHA256) != 64 || strings.Trim(source.SHA256, "0123456789abcdef") != "" {
			return fmt.Errorf("invalid icon attribution")
		}
		if !strings.HasPrefix(source.URL, "https://") {
			return fmt.Errorf("invalid attribution URL")
		}
	}
	return nil
}

func decodePNG(raw []byte) (image.Image, error) {
	if len(raw) > 2<<20 {
		return nil, fmt.Errorf("theme image exceeds 2 MiB")
	}
	c, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || c.Width != c.Height || c.Width < 512 || c.Width > 2048 {
		return nil, fmt.Errorf("theme source must be a square PNG of 512–2048 pixels")
	}
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid theme PNG")
	}
	return img, nil
}

// Render returns a canonical PNG without untrusted ancillary chunks.
func Render(raw []byte, size int) ([]byte, error) {
	return render(raw, size, true)
}

func render(raw []byte, size int, rounded bool) ([]byte, error) {
	if size != 16 && size != 32 && size != 48 && size != 64 && size != 128 && size != 256 && size != 512 {
		return nil, fmt.Errorf("invalid icon size")
	}
	img, err := decodePNG(raw)
	if err != nil {
		return nil, err
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if img.Bounds().Size() == dst.Bounds().Size() {
		draw.Copy(dst, image.Point{}, img, img.Bounds(), draw.Src, nil)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	}
	if rounded {
		RoundCorners(dst)
	}
	var out bytes.Buffer
	err = (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, dst)
	return out.Bytes(), err
}

func Read(raw []byte) (Theme, error) {
	if len(raw) > MaxBytes {
		return Theme{}, fmt.Errorf("theme exceeds 32 MiB")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var t Theme
	if err := d.Decode(&t); err != nil {
		return Theme{}, fmt.Errorf("invalid theme JSON: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Theme{}, fmt.Errorf("trailing theme content")
	}
	if err := t.Validate(); err != nil {
		return Theme{}, err
	}
	return t, nil
}

func Canonical(t Theme) ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	icons := make(map[string][]byte, len(t.Icons))
	for name, raw := range t.Icons {
		// Keep the source square; clipping belongs to each generated display size.
		body, err := render(raw, 512, false)
		if err != nil {
			return nil, err
		}
		icons[name] = body
	}
	t.Icons = icons
	return json.Marshal(t)
}

func Digest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

var builtinCache sync.Map

type builtinResult struct {
	theme Theme
	raw   []byte
	err   error
}

// Built-in artwork is immutable. Decode and normalize it once per process;
// callers must not mutate the returned theme's maps or image bytes.
func builtin(id string) builtinResult {
	if id != "original" && id != "soft" {
		return builtinResult{err: fmt.Errorf("unknown built-in theme")}
	}
	entry, _ := builtinCache.LoadOrStore(id, sync.OnceValue(func() builtinResult {
		raw, err := builtins.ReadFile("builtin/" + id + ".json")
		if err != nil {
			return builtinResult{err: err}
		}
		theme, err := Read(raw)
		if err != nil {
			return builtinResult{err: err}
		}
		raw, err = Canonical(theme)
		return builtinResult{theme: theme, raw: raw, err: err}
	}))
	return entry.(func() builtinResult)()
}

func Builtin(id string) (Theme, error)         { v := builtin(id); return v.theme, v.err }
func BuiltinPackage(id string) ([]byte, error) { v := builtin(id); return v.raw, v.err }
