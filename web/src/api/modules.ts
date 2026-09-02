// REQUIREMENTS: CONSOLE-R-032
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type ModuleList = components["schemas"]["ModuleListResponse"]
type ModuleCatalog = components["schemas"]["ModuleCatalogResponse"]
type ModuleUpdateRequest = components["schemas"]["ModuleUpdateRequest"]
type ModuleJob = components["schemas"]["DeploymentApplyResponse"]

export interface ModuleListResult {
  data: ModuleList
  etag: string
}

function origin(): string {
  return window.location.origin
}

function requireData<T>(data: T | undefined, error: unknown): T {
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

export async function getWorkspaceModules(workspace: string): Promise<ModuleListResult> {
  const { data, error, response } = await api.GET("/api/v1/workspaces/{ws}/modules", {
    params: { path: { ws: workspace } },
  })
  const modules = requireData(data, error)
  const etag = response.headers.get("ETag")
  if (etag === null) throw new APIProblemError({ code: "module_response_invalid" })
  return { data: modules, etag }
}

export async function getModuleCatalog(workspace: string): Promise<ModuleCatalog> {
  const { data, error } = await api.GET("/api/v1/catalog/modules", {
    params: { query: { workspace_id: workspace } },
  })
  return requireData(data, error)
}

export async function updateWorkspaceModules(
  workspace: string,
  request: ModuleUpdateRequest,
  csrf: string,
  idempotencyKey: string,
): Promise<ModuleJob> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/actions/update-modules", {
    params: {
      path: { ws: workspace },
      header: { Origin: origin(), "Idempotency-Key": idempotencyKey },
    },
    headers: { "X-CSRF-Token": csrf },
    body: request,
  })
  return requireData(data, error)
}

export async function configureWorkspaceModule(
  workspace: string,
  module: string,
  action: "enable" | "disable",
  etag: string,
  csrf: string,
  idempotencyKey: string,
): Promise<ModuleJob> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/modules/{module}/actions/{action}", {
    params: {
      path: { ws: workspace, module, action },
      header: { Origin: origin(), "Idempotency-Key": idempotencyKey, "If-Match": etag },
    },
    headers: { "X-CSRF-Token": csrf },
    body: {},
  })
  return requireData(data, error)
}
