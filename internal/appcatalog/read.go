package appcatalog

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// Read consumes the private, frozen deployment input, never a public URL.
func Read(path string) (Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return Catalog{}, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 16<<20))
	d.DisallowUnknownFields()
	var c Catalog
	if err := d.Decode(&c); err != nil {
		return Catalog{}, err
	}
	if c.Schema != Schema {
		return Catalog{}, fmt.Errorf("unsupported application catalog schema")
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return Catalog{}, fmt.Errorf("trailing application catalog data")
	}
	for _, app := range c.Applications {
		sets := []map[string]Icon{app.Icons}
		if len(app.OriginalIcons) > 0 {
			sets = append(sets, app.OriginalIcons)
		}
		for _, icons := range sets {
			for _, size := range IconSizes {
				icon, ok := icons[fmt.Sprint(size)]
				if !ok || icon.Path != "assets/"+icon.SHA256+".png" || len(icon.SHA256) != 64 || strings.Trim(icon.SHA256, "0123456789abcdef") != "" || icon.Width != size || icon.Height != size || icon.MediaType != "image/png" {
					return Catalog{}, fmt.Errorf("invalid catalog icon")
				}
			}
		}
	}
	return c, nil
}

// FromEnvironment also allows isolated hook tests without a Runner artifact.
// Actual catalog consumers always receive the private path.
func FromEnvironment(env map[string]string) (Catalog, error) {
	if path := env["ANAS_APP_CATALOG_FILE"]; path != "" {
		return Read(path)
	}
	return Catalog{Schema: Schema, DefaultLanguage: "en"}, nil
}

func (c Catalog) IAMApplication(client string) (Application, bool) {
	for _, app := range c.Applications {
		if app.Access.Mode == "iam" && app.Access.Client == client {
			return app, true
		}
	}
	return Application{}, false
}

func (c Catalog) Groups() []string {
	var result []string
	seen := map[string]bool{}
	for _, app := range c.Applications {
		if !app.Enabled {
			continue
		}
		for _, group := range app.Groups {
			if !seen[group] {
				seen[group] = true
				result = append(result, group)
			}
		}
	}
	return result
}
