// REQUIREMENTS: CONSOLE-R-120 CONSOLE-R-121 CONSOLE-R-126
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type ConfigCandidate = components["schemas"]["ConfigCandidate"]
type ConfigSnapshot = components["schemas"]["ConfigSnapshotResponse"]
type ConfigValidation = components["schemas"]["ConfigValidationResponse"]
type ConfigPut = components["schemas"]["ConfigPutResponse"]

export interface ConfigSnapshotResult {
  data: ConfigSnapshot
  etag: string | null
}

export interface ConfigPutResult {
  data: ConfigPut
  etag: string
}

function requestOrigin(): string {
  return window.location.origin
}

function requireData<T>(data: T | undefined, error: unknown): T {
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

function validatorETag(validator: string): string {
  return `"${validator}"`
}

export async function getWorkspaceConfig(workspace: string): Promise<ConfigSnapshotResult> {
  const { data, error, response } = await api.GET("/api/v1/workspaces/{ws}/config", {
    params: { path: { ws: workspace } },
  })
  const snapshot = requireData(data, error)
  const etag = response.headers.get("ETag")
  if (snapshot.managed && etag === null) throw new APIProblemError({ code: "config_response_invalid" })
  return { data: snapshot, etag }
}

export async function validateWorkspaceConfig(
  workspace: string,
  candidate: ConfigCandidate,
  csrf: string,
): Promise<ConfigValidation> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/config/validate", {
    params: { path: { ws: workspace }, header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: candidate,
  })
  return requireData(data, error)
}

export async function putWorkspaceConfig(
  workspace: string,
  candidate: ConfigCandidate,
  csrf: string,
  managed: boolean,
  etag: string | null,
): Promise<ConfigPutResult> {
  const header: {
    Origin: string
    "If-Match"?: string
    "If-None-Match"?: "*"
  } = { Origin: requestOrigin() }
  if (managed) {
    if (etag === null) throw new APIProblemError({ code: "config_precondition_required" })
    header["If-Match"] = etag
  } else {
    header["If-None-Match"] = "*"
  }
  const { data, error, response } = await api.PUT("/api/v1/workspaces/{ws}/config", {
    params: { path: { ws: workspace }, header },
    headers: { "X-CSRF-Token": csrf },
    body: candidate,
  })
  const saved = requireData(data, error)
  return { data: saved, etag: response.headers.get("ETag") ?? validatorETag(saved.validator) }
}
