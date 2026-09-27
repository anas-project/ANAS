package httpapi

import (
	"sort"
	"strings"
	"testing"
)

// INCUS-R-083: bulk data only travels on paths the action opens itself. The
// console publishes no artifact download endpoint and no response carries an
// opaque binary payload. The single non-JSON attachment is the small public
// internal CA certificate; anything else needs the section 13 decision in the
// action ABI design (lifetime, reuse, non-browser clients) before this list
// may grow.
func TestNoArtifactDownloadResponseMediaTypes(t *testing.T) {
	document := readOpenAPIDocument(t)
	allowed := map[string]bool{
		"application/json":         true,
		"application/problem+json": true,
		"text/event-stream":        true,
		"text/html":                true,
		"text/css":                 true,
		"text/javascript":          true,
	}
	var attachments []string
	for path, rawItem := range objectAt(t, document, "paths") {
		item, _ := rawItem.(map[string]any)
		for method, rawOperation := range item {
			operation, ok := rawOperation.(map[string]any)
			if !ok {
				continue
			}
			responses, _ := operation["responses"].(map[string]any)
			for status, rawResponse := range responses {
				response, _ := rawResponse.(map[string]any)
				content, _ := response["content"].(map[string]any)
				for media := range content {
					route := strings.ToUpper(method) + " " + path
					if media == "application/pem-certificate-chain" {
						attachments = append(attachments, route)
						continue
					}
					if !allowed[media] {
						t.Errorf("%s %s responds with %s; artifact downloads are not offered (INCUS-R-083)", route, status, media)
					}
				}
			}
		}
	}
	sort.Strings(attachments)
	if len(attachments) != 1 || attachments[0] != "GET /api/v1/system/ca" {
		t.Fatalf("certificate attachments = %v, want only the public internal CA", attachments)
	}
}
