// Package moduledocs contains shared renderers for generated Module documentation.
package moduledocs

import (
	"fmt"
	"sort"
	"strings"
)

// CatalogEntry is the manifest projection displayed in the Module catalog.
type CatalogEntry struct {
	Name        string
	Title       string
	Version     string
	Revision    int
	Status      string
	Category    string
	Description string
}

// RenderCatalog renders the current Module catalog in Chinese or English.
// Entries are copied before sorting so callers retain ownership of their input.
func RenderCatalog(entries []CatalogEntry, english bool) []byte {
	sorted := append([]CatalogEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	var out strings.Builder
	if english {
		out.WriteString("---\neditLink: false\nlastUpdated: false\n---\n\n# Module catalog\n\nThis page is generated from the current Module manifests. The checked-in page and documentation build use the same renderer; do not maintain a second mapping list.\n\n")
	} else {
		out.WriteString("---\neditLink: false\nlastUpdated: false\n---\n\n# Module 目录\n\n本页由当前 Module manifests 生成；仓库页面与文档构建期使用同一个 renderer，不维护第二份映射清单。\n\n")
	}
	out.WriteString("| Module | Version | Status | Category | Description |\n| --- | --- | --- | --- | --- |\n")
	for _, entry := range sorted {
		link := "/reference/modules/" + entry.Name + "/"
		if english {
			link = "/en" + link
		}
		fmt.Fprintf(
			&out,
			"| [%s](%s) | `%s-r%d` | `%s` | `%s` | %s |\n",
			escapeTableCell(entry.Title),
			link,
			escapeTableCell(entry.Version),
			entry.Revision,
			escapeTableCell(entry.Status),
			escapeTableCell(entry.Category),
			escapeTableCell(entry.Description),
		)
	}
	return []byte(out.String())
}

func escapeTableCell(value string) string {
	return strings.ReplaceAll(value, "|", "\\|")
}
