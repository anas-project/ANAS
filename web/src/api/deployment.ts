// REQUIREMENTS: CONSOLE-R-054 CONSOLE-R-115 CONSOLE-R-117 CONSOLE-R-118 CONSOLE-R-122 CONSOLE-R-131
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type LocalStepUp = components["schemas"]["LocalStepUpResponse"]
type DeploymentPlan = components["schemas"]["DeploymentPlanResponse"]
type DeploymentApplyRequest = components["schemas"]["DeploymentApplyRequest"]
type DeploymentApply = components["schemas"]["DeploymentApplyResponse"]
type JobDetail = components["schemas"]["JobDetailResponse"]
type JobList = components["schemas"]["JobListResponse"]

function requestOrigin(): string {
  return window.location.origin
}

function requireData<T>(data: T | undefined, error: unknown): T {
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

export async function issueDeploymentStepUp(
  workspace: string,
  password: string | undefined,
  csrf: string,
  deploymentID?: string,
): Promise<LocalStepUp> {
  const body: components["schemas"]["LocalStepUpRequest"] = {
	...(password === undefined ? {} : { password }),
    action: "deployment.apply",
    workspace_id: workspace,
    ...(deploymentID === undefined || deploymentID === "" ? {} : { deployment_id: deploymentID }),
  }
  const { data, error } = await api.POST("/api/v1/auth/step-up", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body,
  })
  return requireData(data, error)
}

export async function planWorkspaceDeployment(
  workspace: string,
  csrf: string,
  stepUpProof?: string,
  deploymentID?: string,
): Promise<DeploymentPlan> {
  const body: components["schemas"]["DeploymentPlanRequest"] = {
    ...(stepUpProof === undefined || stepUpProof === "" ? {} : { step_up_proof: stepUpProof }),
    ...(deploymentID === undefined || deploymentID === "" ? {} : { deployment_id: deploymentID }),
  }
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/plans", {
    params: { path: { ws: workspace }, header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body,
  })
  return requireData(data, error)
}

export async function applyWorkspaceDeployment(
  workspace: string,
  request: DeploymentApplyRequest,
  csrf: string,
  idempotencyKey: string,
): Promise<DeploymentApply> {
  const { data, error } = await api.POST("/api/v1/workspaces/{ws}/actions/apply", {
    params: {
      path: { ws: workspace },
      header: { Origin: requestOrigin(), "Idempotency-Key": idempotencyKey },
    },
    headers: { "X-CSRF-Token": csrf },
    body: request,
  })
  return requireData(data, error)
}

export async function getJob(jobID: string): Promise<JobDetail> {
  const { data, error } = await api.GET("/api/v1/jobs/{id}", {
    params: { path: { id: jobID } },
  })
  return requireData(data, error)
}

export async function listJobs(limit = 25, cursor?: string): Promise<JobList> {
  const query: { limit: number; cursor?: string } = { limit }
  if (cursor !== undefined && cursor !== "") query.cursor = cursor
  const { data, error } = await api.GET("/api/v1/jobs", { params: { query } })
  return requireData(data, error)
}

export function newIdempotencyKey(): string {
  const bytes = new Uint8Array(16)
  crypto.getRandomValues(bytes)
  return `web-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`
}
