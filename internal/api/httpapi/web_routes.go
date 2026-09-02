package httpapi

// REQUIREMENTS: CONSOLE-R-008 CONSOLE-R-085 CONSOLE-R-113

import (
	"net/http"

	"github.com/anas-project/ANAS/internal/webui"
)

const (
	consoleMainScriptPath     = "/assets/main.js"
	consoleMainStylesPath     = "/assets/main.css"
	consoleRecoveryPath       = "/emergency"
	consoleRecoveryScriptPath = "/assets/emergency.js"
	consoleRecoveryStylesPath = "/assets/emergency.css"
)

func (h *handler) consoleRoot(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	state, ok := ConsoleStateFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusInternalServerError, "state_unavailable", "control-plane state is unavailable")
		return
	}
	if r.TLS == nil && (state == StateEnrollment || state == StateFull) {
		h.redirectToCanonicalHTTPS(w, r, params)
		return
	}
	serveWebAsset(w, webui.MainIndex)
}

func webAssetHandler(asset webui.Asset) routeHandler {
	return func(w http.ResponseWriter, r *http.Request, _ map[string]string) {
		if _, ok := supportedQuery(w, r); !ok {
			return
		}
		serveWebAsset(w, asset)
	}
}

func serveWebAsset(w http.ResponseWriter, asset webui.Asset) {
	content, err := webui.Read(asset)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError, "console_asset_unavailable", "console asset is unavailable")
		return
	}
	w.Header().Set("Content-Type", content.ContentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content.Body)
}
