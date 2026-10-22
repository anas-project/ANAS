package appcatalog

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/anas-project/ANAS/internal/apptheme"
)

// ConfiguredTheme reads only local, reviewed input. The asset reader forbids
// symlinks and private directory components just as it does for module icons.
func ConfiguredTheme(config Config, root string) (apptheme.Theme, error) {
	if config.IconTheme == "custom" {
		raw, err := readAssetLimit(root, config.ThemeFile, apptheme.MaxBytes)
		if err != nil {
			return apptheme.Theme{}, err
		}
		theme, err := apptheme.Read(raw)
		if err != nil {
			return apptheme.Theme{}, err
		}
		if theme.ID == "original" || theme.ID == "soft" {
			return apptheme.Theme{}, fmt.Errorf("custom theme must use its own ID")
		}
		return theme, nil
	}
	id := config.IconTheme
	if id == "" {
		id = "original"
	}
	return apptheme.Builtin(id)
}

// ApplyIconTheme freezes the selected images into the existing directory.
// Casdoor and the portal therefore consume identical default icon bytes.
func ApplyIconTheme(config Config, root string, catalog Catalog, assets Assets, files map[string][]byte) (Catalog, Assets, map[string][]byte, error) {
	theme, err := ConfiguredTheme(config, root)
	if err != nil {
		return Catalog{}, Assets{}, nil, err
	}
	catalog.IconTheme, catalog.ThemeName, catalog.ThemeColors = theme.ID, theme.Name, theme.Colors
	catalog.ThemeSources = map[string]apptheme.Attribution{}
	if theme.ID == "original" {
		return catalog, assets, files, nil
	}
	records := map[string]AssetFile{}
	for _, record := range assets.Files {
		records[record.Path] = record
	}
	for i := range catalog.Applications {
		app := &catalog.Applications[i]
		raw, found := theme.Icons[app.Name]
		if !found {
			continue
		}
		app.OriginalIcons = app.Icons
		app.Icons = map[string]Icon{}
		if source, ok := theme.Sources[app.Name]; ok {
			catalog.ThemeSources[app.Name] = source
		}
		for _, size := range IconSizes {
			body, err := apptheme.Render(raw, size)
			if err != nil {
				return Catalog{}, Assets{}, nil, err
			}
			hash := apptheme.Digest(body)
			path := "assets/" + hash + ".png"
			icon := Icon{Path: path, SHA256: hash, MediaType: "image/png", Width: size, Height: size}
			app.Icons[strconv.Itoa(size)] = icon
			files[path] = body
			record := records[path]
			record.Icon = icon
			record.Sources = append(record.Sources, AssetSource{Application: app.Name, Path: "theme:" + theme.ID, SHA256: apptheme.Digest(raw), Generated: true})
			records[path] = record
		}
	}
	total := 0
	for _, data := range files {
		total += len(data)
	}
	if total > maxBundleBytes {
		return Catalog{}, Assets{}, nil, fmt.Errorf("theme and original icons exceed 64 MiB")
	}
	assets.Files = []AssetFile{}
	paths := make([]string, 0, len(records))
	for path := range records {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		assets.Files = append(assets.Files, records[path])
	}
	return catalog, assets, files, nil
}
