package appcatalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/anas-project/ANAS/internal/apptheme"

	"golang.org/x/image/draw"
)

var IconSizes = [...]int{16, 32, 48, 64, 128, 256, 512}

const (
	maxInputBytes       = 2 << 20
	maxApplicationBytes = 4 << 20
	maxBundleBytes      = 64 << 20
	ScalerVersion       = "golang.org/x/image@v0.46.0/CatmullRom/png-best-compression/rounded-v1"
)

type AssetSource struct {
	Application string `json:"application"`
	Path        string `json:"path"`
	SHA256      string `json:"sha256"`
	Generated   bool   `json:"generated"`
}

type AssetFile struct {
	Icon
	Sources []AssetSource `json:"sources"`
}

type Assets struct {
	Schema    string      `json:"schema"`
	Generator string      `json:"generator"`
	Files     []AssetFile `json:"files"`
}

func validateIconInput(input IconInput) error {
	if input.Source != "" {
		if err := validateAssetPath(input.Source); err != nil {
			return err
		}
	}
	for size, path := range input.Sizes {
		found := false
		for _, allowed := range IconSizes {
			if size == allowed {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("icons.sizes contains unsupported size")
		}
		if err := validateAssetPath(path); err != nil {
			return err
		}
	}
	if input.Source == "" && len(input.Sizes) > 0 {
		return fmt.Errorf("icons.sizes requires a source PNG")
	}
	return nil
}

func validateAssetPath(path string) error {
	if path == "" || filepath.IsAbs(path) || strings.ContainsAny(path, "\\\x00\r\n") || !filepath.IsLocal(path) {
		return fmt.Errorf("icon path must stay inside its source root")
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		switch strings.ToLower(part) {
		case "", ".", "..", "secret", "secrets", "data", ".anas", "certs":
			return fmt.Errorf("icon path contains a forbidden component")
		}
	}
	return nil
}

// readAsset pins each opened directory and compares file identities. No symlink
// is followed, including a replacement between inspection and opening.
func readAsset(rootPath, path string) ([]byte, error) {
	return readAssetLimit(rootPath, path, maxInputBytes)
}

func readAssetLimit(rootPath, path string, limit int64) ([]byte, error) {
	if err := validateAssetPath(path); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	current := root
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts[:len(parts)-1] {
		before, err := current.Lstat(part)
		if err != nil {
			return nil, err
		}
		if !before.IsDir() {
			return nil, fmt.Errorf("icon ancestor must be a directory, not a link")
		}
		next, err := current.OpenRoot(part)
		if err != nil {
			return nil, err
		}
		defer next.Close()
		after, err := next.Stat(".")
		if err != nil || !os.SameFile(before, after) {
			return nil, fmt.Errorf("icon directory changed during open")
		}
		current = next
	}
	name := parts[len(parts)-1]
	before, err := current.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Size() > limit {
		return nil, fmt.Errorf("asset must be a regular file within its size limit")
	}
	file, err := openAssetFile(current, name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, fmt.Errorf("icon file changed during open")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("asset exceeds its size limit")
	}
	return data, nil
}

// ReadIcon serves a frozen icon only after the caller has authorized its owning
// application. It reuses the no-link bundle reader and verifies the content.
func ReadIcon(rootPath string, icon Icon) ([]byte, error) {
	if icon.Path != "assets/"+icon.SHA256+".png" || len(icon.SHA256) != 64 || strings.Trim(icon.SHA256, "0123456789abcdef") != "" || icon.MediaType != "image/png" {
		return nil, fmt.Errorf("invalid frozen icon")
	}
	data, err := readAsset(rootPath, icon.Path)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if hex.EncodeToString(hash[:]) != icon.SHA256 {
		return nil, fmt.Errorf("frozen icon digest mismatch")
	}
	return data, nil
}

func decodeIcon(data []byte, size int) (image.Image, error) {
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("icon is not a valid PNG")
	}
	if config.Width != config.Height || config.Width > 2048 || config.Width < 1 || (size == 0 && config.Width < 512) || (size != 0 && config.Width != size) {
		return nil, fmt.Errorf("PNG dimensions must match the requested square size (source: 512–2048)")
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("PNG pixels could not be decoded")
	}
	return img, nil
}

func placeholder() image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			shade := color.NRGBA{R: 35, G: 67, B: 103, A: 255}
			if x > 112 && x < 400 && y > 112 && y < 400 {
				shade = color.NRGBA{R: 227, G: 237, B: 247, A: 255}
			}
			if x > 160 && x < 352 && y > 160 && y < 352 {
				shade = color.NRGBA{R: 35, G: 67, B: 103, A: 255}
			}
			img.SetNRGBA(x, y, shade)
		}
	}
	return img
}

func encodeIcon(img image.Image, size int) ([]byte, error) {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	if img.Bounds().Size() == dst.Bounds().Size() {
		draw.Copy(dst, image.Point{}, img, img.Bounds(), draw.Src, nil)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Src, nil)
	}
	apptheme.RoundCorners(dst)
	var body bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(&body, dst); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// BuildAssets returns deterministic content-addressed bytes and source evidence.
// Re-encoding also removes untrusted ancillary chunks from input PNG files.
func BuildAssets(catalog Catalog, inputs []Input) (Catalog, Assets, map[string][]byte, error) {
	assets := Assets{Schema: "anas.app-catalog-assets/v1", Generator: ScalerVersion, Files: []AssetFile{}}
	files := map[string][]byte{}
	records := map[string]AssetFile{}
	total := 0
	byName := map[string]Input{}
	for _, input := range inputs {
		byName[input.Application.Name] = input
	}
	for i := range catalog.Applications {
		app := &catalog.Applications[i]
		input, ok := byName[app.Name]
		if !ok {
			return Catalog{}, Assets{}, nil, fmt.Errorf("missing icon input")
		}
		if err := validateIconInput(input.Icons); err != nil {
			return Catalog{}, Assets{}, nil, err
		}
		base := placeholder()
		sourcePath := "builtin:placeholder-v1"
		sourceHash := ""
		isPlaceholder := input.Icons.Source == ""
		if !isPlaceholder {
			body, err := readAsset(input.Root, input.Icons.Source)
			if err != nil {
				return Catalog{}, Assets{}, nil, fmt.Errorf("application %s icon source: %w", app.Name, err)
			}
			base, err = decodeIcon(body, 0)
			if err != nil {
				return Catalog{}, Assets{}, nil, err
			}
			sourcePath = input.Icons.Source
			sourceHash = fmt.Sprintf("%x", sha256.Sum256(body))
		}
		app.Icons = map[string]Icon{}
		applicationBytes := 0
		for _, size := range IconSizes {
			img := base
			path := sourcePath
			hash := sourceHash
			generated := true
			if custom, ok := input.Icons.Sizes[size]; ok {
				body, err := readAsset(input.Root, custom)
				if err != nil {
					return Catalog{}, Assets{}, nil, err
				}
				img, err = decodeIcon(body, size)
				if err != nil {
					return Catalog{}, Assets{}, nil, err
				}
				path = custom
				hash = fmt.Sprintf("%x", sha256.Sum256(body))
				generated = false
			}
			body, err := encodeIcon(img, size)
			if err != nil {
				return Catalog{}, Assets{}, nil, err
			}
			applicationBytes += len(body)
			if applicationBytes > maxApplicationBytes {
				return Catalog{}, Assets{}, nil, fmt.Errorf("application icons exceed 4 MiB")
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(body))
			assetPath := "assets/" + digest + ".png"
			icon := Icon{Path: assetPath, MediaType: "image/png", Width: size, Height: size, SHA256: digest}
			app.Icons[strconv.Itoa(size)] = icon
			if _, exists := files[assetPath]; !exists {
				total += len(body)
				if total > maxBundleBytes {
					return Catalog{}, Assets{}, nil, fmt.Errorf("icon bundle exceeds 64 MiB")
				}
				files[assetPath] = body
			}
			record := records[assetPath]
			record.Icon = icon
			record.Sources = append(record.Sources, AssetSource{Application: app.Name, Path: path, SHA256: hash, Generated: generated})
			records[assetPath] = record
		}
	}
	paths := []string{}
	for path := range records {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		assets.Files = append(assets.Files, records[path])
	}
	return catalog, assets, files, nil
}

// WriteBundle writes only to a fresh deployment directory. Runner's artifact
// sealing and promote operation provide publication, rollback and retention.
func WriteBundle(dir string, catalog Catalog, assets Assets, files map[string][]byte) (string, error) {
	if err := os.Mkdir(dir, 0755); err != nil {
		return "", err
	}
	// The bundle is bind-mounted into consumers running with a different UID.
	// Its declared modes must survive a restrictive producer umask; the private
	// deployment parent and authenticated APIs still control access.
	if err := os.Chmod(dir, 0755); err != nil {
		return "", err
	}
	if err := os.Mkdir(filepath.Join(dir, "assets"), 0755); err != nil {
		return "", err
	}
	if err := os.Chmod(filepath.Join(dir, "assets"), 0755); err != nil {
		return "", err
	}
	var catalogBytes []byte
	for name, document := range map[string]any{"catalog.json": catalog, "assets.json": assets} {
		body, err := json.MarshalIndent(document, "", "  ")
		if err != nil {
			return "", err
		}
		body = append(body, '\n')
		if name == "catalog.json" {
			catalogBytes = body
		}
		if err := os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
			return "", err
		}
		if err := os.Chmod(filepath.Join(dir, name), 0644); err != nil {
			return "", err
		}
	}
	for path, body := range files {
		digest := fmt.Sprintf("%x", sha256.Sum256(body))
		if path != "assets/"+digest+".png" {
			return "", fmt.Errorf("invalid asset content address")
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), body, 0644); err != nil {
			return "", err
		}
		if err := os.Chmod(filepath.Join(dir, filepath.FromSlash(path)), 0644); err != nil {
			return "", err
		}
	}
	assetBytes, err := json.Marshal(assets)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(append(catalogBytes, assetBytes...))), nil
}
