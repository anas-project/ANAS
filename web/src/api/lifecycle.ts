// REQUIREMENTS: CONSOLE-R-034 CONSOLE-R-054 CONSOLE-R-124
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type LifecycleAction = components["schemas"]["LifecyclePreview"]["action"]
type LifecycleRequest = components["schemas"]["LifecycleActionRequest"]
type LifecyclePreview = components["schemas"]["LifecyclePreviewResponse"]
type LifecycleJob = components["schemas"]["DeploymentApplyResponse"]
type WorkspaceStatus = components["schemas"]["WorkspaceStatusResponse"]

function origin(): string {
  return window.location.origin
}

export async function getWorkspaceRuntime(workspace: string): Promise<WorkspaceStatus> {
  const { data, error } = await api.GET("/api/v1/workspaces/{ws}/status", {
    params: { path: { ws: workspace } },
  })
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

export async function previewModuleLifecycle(
  workspace: string,
  action: LifecycleAction,
  modules: string[],
  csrf: string,
): Promise<LifecyclePreview> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/modules/actions/{action}", {
    params: { path: { ws: workspace, action }, header: { Origin: origin() } },
    headers: { "X-CSRF-Token": csrf },
    body: { modules },
  })
  if (error || data === undefined || !("preview" in data)) throw new APIProblemError(error ?? { code: "lifecycle_response_invalid" })
  return data
}

export async function executeModuleLifecycle(
  workspace: string,
  action: LifecycleAction,
  request: LifecycleRequest,
  csrf: string,
  idempotencyKey: string,
): Promise<LifecycleJob> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/modules/actions/{action}", {
    params: {
      path: { ws: workspace, action },
      header: { Origin: origin(), "Idempotency-Key": idempotencyKey },
    },
    headers: { "X-CSRF-Token": csrf },
    body: request,
  })
  if (error || data === undefined || !("job" in data)) throw new APIProblemError(error ?? { code: "lifecycle_response_invalid" })
  return data
}
