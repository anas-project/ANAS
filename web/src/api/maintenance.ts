// REQUIREMENTS: CONSOLE-R-047 CONSOLE-R-123 CONSOLE-R-127 CONSOLE-R-132 CONSOLE-R-133 CONSOLE-R-144 CONSOLE-R-150
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

export type SnapshotList = components["schemas"]["SnapshotListResponse"]
export type SnapshotRecord = components["schemas"]["SnapshotRecord"]
export type BackupPlanRequest = components["schemas"]["BackupPlanRequest"]
export type BackupPlanResponse = components["schemas"]["BackupPlanResponse"]
export type BackupList = components["schemas"]["BackupListResponse"]
export type BackupRecord = components["schemas"]["BackupRecord"]
export type LocalAdminList = components["schemas"]["LocalAdminListResponse"]
export type LocalAdminRecord = components["schemas"]["LocalAdminRecord"]
export type LocalAdminReveal = components["schemas"]["LocalAdminRevealResponse"]
export type TerminalActionRequest = components["schemas"]["TerminalActionRequest"]
export type TerminalActionDescriptor = components["schemas"]["TerminalActionPreviewResponse"]
type Job = components["schemas"]["DeploymentApplyResponse"]
type StepUp = components["schemas"]["LocalStepUpResponse"]

function requestOrigin(): string {
  return window.location.origin
}

function requireData<T>(data: T | undefined, error: unknown): T {
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

export async function listSnapshots(workspace: string): Promise<SnapshotList> {
  const { data, error } = await api.GET("/api/v1/workspaces/{ws}/snapshots", {
    params: { path: { ws: workspace } },
  })
  return requireData(data, error)
}

export async function createSnapshot(
  workspace: string,
  label: string,
  includeUserData: boolean,
  csrf: string,
  idempotencyKey: string,
): Promise<Job> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/snapshots", {
    params: { path: { ws: workspace }, header: { Origin: requestOrigin(), "Idempotency-Key": idempotencyKey } },
    headers: { "X-CSRF-Token": csrf },
    body: { label, include_userdata: includeUserData },
  })
  return requireData(data, error)
}

export async function changeSnapshot(
  workspace: string,
  snapshotID: string,
  action: "pin" | "unpin" | "verify",
  label: string,
  csrf: string,
  idempotencyKey: string,
): Promise<Job> {
  const body = action === "verify" ? {} : label === "" ? {} : { label }
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/snapshots/{id}/actions/{action}", {
    params: {
      path: { ws: workspace, id: snapshotID, action },
      header: { Origin: requestOrigin(), "Idempotency-Key": idempotencyKey },
    },
    headers: { "X-CSRF-Token": csrf },
    body,
  })
  return requireData(data, error)
}

export async function planBackup(workspace: string, request: BackupPlanRequest, csrf: string): Promise<BackupPlanResponse> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/backup-plans", {
    params: { path: { ws: workspace }, header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: request,
  })
  return requireData(data, error)
}

export async function listBackups(workspace: string, targetID: string): Promise<BackupList> {
  const { data, error } = await api.GET("/api/v1/workspaces/{ws}/backups", {
    params: { path: { ws: workspace }, query: { target_id: targetID } },
  })
  return requireData(data, error)
}

export async function listLocalAdmins(workspace: string): Promise<LocalAdminList> {
  const { data, error } = await api.GET("/api/v1/workspaces/{ws}/local-admins", {
    params: { path: { ws: workspace } },
  })
  return requireData(data, error)
}

export async function rotateLocalAdmin(
  workspace: string,
  account: LocalAdminRecord,
  csrf: string,
  idempotencyKey: string,
): Promise<Job> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/local-admins/{module}/{account}/actions/rotate", {
    params: {
      path: { ws: workspace, module: account.module, account: account.account },
      header: { Origin: requestOrigin(), "Idempotency-Key": idempotencyKey },
    },
    headers: { "X-CSRF-Token": csrf },
    body: {},
  })
  return requireData(data, error)
}

export async function issueLocalAdminRevealStepUp(
  workspace: string,
  targetID: string,
  password: string | undefined,
  csrf: string,
): Promise<StepUp> {
  const body: components["schemas"]["LocalStepUpRequest"] = {
    ...(password === undefined ? {} : { password }),
    action: "local_admin.reveal",
    workspace_id: workspace,
    target_id: targetID,
  }
  const { data, error } = await api.POST("/api/v1/auth/step-up", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body,
  })
  return requireData(data, error)
}

export async function revealLocalAdmin(
  workspace: string,
  account: LocalAdminRecord,
  proof: string,
  csrf: string,
): Promise<LocalAdminReveal> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/local-admins/{module}/{account}/reveal", {
    params: {
      path: { ws: workspace, module: account.module, account: account.account },
      header: { Origin: requestOrigin() },
    },
    headers: { "X-CSRF-Token": csrf },
    body: { step_up_proof: proof },
  })
  return requireData(data, error)
}

export async function previewTerminalAction(
  workspace: string,
  request: TerminalActionRequest,
  csrf: string,
): Promise<TerminalActionDescriptor> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/terminal-action-previews", {
    params: { path: { ws: workspace }, header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: request,
  })
  return requireData(data, error)
}
