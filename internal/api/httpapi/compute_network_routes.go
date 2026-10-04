package httpapi

import (
	"net/http"

	"github.com/anas-project/ANAS/internal/application"
)

// computeLeaseNetworkResponse is the console's read-only lease network view
// (INCUS-R-128). It carries no credential, key or certificate.
type computeLeaseNetworkResponse struct {
	APIVersion       string                            `json:"api_version"`
	WorkspaceID      string                            `json:"workspace_id"`
	ActiveDeployment *string                           `json:"active_deployment"`
	Leases           []application.ComputeLeaseNetwork `json:"leases"`
}

func (h *handler) listComputeLeaseNetworks(w http.ResponseWriter, r *http.Request, params map[string]string) {
	if _, ok := supportedQuery(w, r); !ok {
		return
	}
	workspacePath, ok := h.registry.Resolve(params["ws"])
	if !ok {
		writeProblem(w, http.StatusNotFound, "workspace_not_found", "workspace was not found")
		return
	}
	service := h.deploymentHTTP.computeNetworkFactory(workspacePath)
	if service == nil {
		writeProblem(w, http.StatusServiceUnavailable, "compute_network_unavailable", "the lease network view is unavailable")
		return
	}
	result, err := service.ListLeaseNetworks(r.Context())
	if err != nil {
		writeApplicationError(w, err)
		return
	}
	if result.Leases == nil {
		result.Leases = []application.ComputeLeaseNetwork{}
	}
	writeJSON(w, http.StatusOK, computeLeaseNetworkResponse{
		APIVersion: APIVersion, WorkspaceID: params["ws"], ActiveDeployment: result.ActiveDeployment, Leases: result.Leases,
	})
}
