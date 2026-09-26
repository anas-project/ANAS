import type { HostActionJob, IncusHostPhase, IncusHostParameters } from "../api/maintenance"
import { APIProblemError } from "../api/problems"

export interface IncusInput {
  workspace: string
  phase: IncusHostPhase
  csrf: string
  request: { interface: "incus_container" | "incus_vm"; storage_size_gib: number }
}
export interface IncusReviewedPlan {
  id: string
  expiresAt: number
  parameters: IncusHostParameters
  steps: { id: string; phase: string; effect: string; destructive: boolean }[]
  blockers: string[]
}
export interface IncusFlowState {
  status: "idle" | "planning" | "ready" | "applying" | "submitted" | "error"
  plan: IncusReviewedPlan | null
  jobID: string
  errorCode: string | null
}
export interface IncusFlowAPI {
  plan(input: IncusInput): Promise<HostActionJob>
  get(id: string, signal: AbortSignal): Promise<HostActionJob>
  confirm(input: IncusInput, planID: string): Promise<{ token: string; expires_at: string }>
  apply(input: IncusInput, planID: string, token: string, parameters: IncusHostParameters): Promise<HostActionJob>
}

function object(value: unknown): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) throw new APIProblemError({ code: "maintenance_response_invalid" })
  return value as Record<string, unknown>
}
function strings(value: unknown): string[] {
  if (value === undefined) return []
  if (!Array.isArray(value) || value.length > 128 || value.some((v) => typeof v !== "string" || v.length > 512)) throw new APIProblemError({ code: "maintenance_response_invalid" })
  return value as string[]
}

export function readIncusPlan(response: HostActionJob, input: IncusInput, expectedID: string): IncusReviewedPlan {
  const job = response.job
  if (job.id !== expectedID || job.workspace_id !== input.workspace || job.status !== "succeeded" || job.mutating) throw new APIProblemError({ code: "maintenance_response_invalid" })
  const value = object(object(job.result).value)
  const inspect = object(value.inspect)
  const plan = object(inspect.plan)
  const parameters = object(value.parameters)
  const binding = object(parameters.binding)
  const request = object(parameters.request)
  const digest = plan.digest
  const expiresAt = Date.parse(job.created_at) + 300_000
  if (value.schema !== "anas.host-action.incus/v1" || inspect.schema !== "anas.incus-host-provision/v1" || plan.schema !== inspect.schema ||
      value.action !== `incus.${input.phase}` || value.phase !== input.phase ||
      parameters.schema !== value.schema || binding.schema !== "anas.incus-host-provision/v1" || binding.phase !== input.phase ||
      binding.destructive !== true || typeof digest !== "string" || !/^[a-f0-9]{64}$/.test(digest) || binding.plan_digest !== digest ||
      (request.interface ?? "incus_container") !== input.request.interface || (request.storage_size_gib ?? 64) !== input.request.storage_size_gib ||
      request.remove_packages !== undefined || (request.skip !== undefined && request.skip !== false) || !Number.isFinite(expiresAt) ||
      (request.chinese_speedup !== undefined && typeof request.chinese_speedup !== "boolean") ||
      !Array.isArray(plan.steps) || plan.steps.length > 128) throw new APIProblemError({ code: "maintenance_response_invalid" })
  const steps = plan.steps.map((raw) => {
    const step = object(raw)
    if (typeof step.id !== "string" || typeof step.phase !== "string" || typeof step.effect !== "string" || step.effect.length > 512 || typeof step.destructive !== "boolean") {
      throw new APIProblemError({ code: "maintenance_response_invalid" })
    }
    return { id: step.id, phase: step.phase, effect: step.effect, destructive: step.destructive }
  }).filter((step) => step.phase === input.phase)
  const accepted: IncusHostParameters = {
    schema: "anas.host-action.incus/v1",
    // Preserve the server's optional-field shape after validating each value.
    // Review and apply must not silently rewrite a displayed request.
    request: {
      ...(request.interface !== undefined ? { interface: input.request.interface } : {}),
      ...(request.storage_size_gib !== undefined ? { storage_size_gib: input.request.storage_size_gib } : {}),
      ...(request.skip !== undefined ? { skip: false } : {}),
      ...(request.chinese_speedup !== undefined ? { chinese_speedup: request.chinese_speedup as boolean } : {}),
    },
    binding: { schema: "anas.incus-host-provision/v1", phase: input.phase, plan_digest: digest, destructive: true },
  }
  return { id: expectedID, expiresAt, parameters: accepted, steps, blockers: strings(plan.blockers) }
}

// Approval is for the exact server-produced plan. Tokens exist only inside
// execute(), never in UI fields, URLs, browser storage or the observable state.
export class IncusApprovalFlow {
  state: IncusFlowState = { status: "idle", plan: null, jobID: "", errorCode: null }
  private revision = 0
  private controller: AbortController | null = null
  private expiryTimer: ReturnType<typeof setTimeout> | undefined
  private pollTimer: ReturnType<typeof setTimeout> | undefined
  private stopPoll: (() => void) | undefined
  private disposed = false
  private preparedInput: IncusInput | null = null

  constructor(private api: IncusFlowAPI, private changed: (state: IncusFlowState) => void, private now: () => number = Date.now) {}

  private publish(next: IncusFlowState): void { this.state = next; this.changed(next) }
  reset(): void {
    this.revision++
    this.controller?.abort()
    this.controller = null
    clearTimeout(this.expiryTimer)
    clearTimeout(this.pollTimer)
    this.stopPoll?.()
    this.stopPoll = undefined
    this.preparedInput = null
    this.publish({ status: "idle", plan: null, jobID: "", errorCode: null })
  }
  dispose(): void { this.disposed = true; this.reset() }

  async prepare(input: IncusInput): Promise<void> {
    if (this.disposed) return
    this.reset()
    const revision = this.revision
    const frozen: IncusInput = { ...input, request: { ...input.request } }
    this.preparedInput = frozen
    const controller = new AbortController()
    this.controller = controller
    this.publish({ status: "planning", plan: null, jobID: "", errorCode: null })
    try {
      let response = await this.api.plan(frozen)
      if (revision !== this.revision || this.disposed) return
      const id = response.job.id
      if (!id || response.job.workspace_id !== frozen.workspace) throw new APIProblemError({ code: "maintenance_response_invalid" })
      const deadline = this.now() + 120_000
      while (response.job.status === "queued" || response.job.status === "running") {
        if (this.now() >= deadline) throw new APIProblemError({ code: "deadline_exceeded" })
        await new Promise<void>((resolve) => { this.stopPoll = resolve; this.pollTimer = setTimeout(resolve, 750) })
        this.stopPoll = undefined
        if (revision !== this.revision || this.disposed) return
        response = await this.api.get(id, controller.signal)
        if (revision !== this.revision || this.disposed) return
        if (response.job.id !== id || response.job.workspace_id !== frozen.workspace) throw new APIProblemError({ code: "maintenance_response_invalid" })
      }
      if (response.job.status !== "succeeded") throw new APIProblemError({ code: response.job.error?.code ?? "request_failed" })
      const plan = readIncusPlan(response, frozen, id)
      if (plan.expiresAt <= this.now()) throw new APIProblemError({ code: "confirmation_invalid" })
      this.publish({ status: "ready", plan, jobID: id, errorCode: null })
      // Refresh the plan, not an old approval. A new plan ID resets the UI's
      // consent checkbox. This timer never calls confirm() or apply().
      this.expiryTimer = setTimeout(() => { void this.prepare(frozen) }, Math.max(1, plan.expiresAt - this.now()))
    } catch (error) {
      if (revision !== this.revision || this.disposed) return
      this.publish({ status: "error", plan: null, jobID: this.state.jobID, errorCode: error instanceof APIProblemError ? error.code : "request_failed" })
    }
  }

  async execute(input: IncusInput, approved: boolean): Promise<string | null> {
    const plan = this.state.plan
    if (!approved || this.disposed || this.state.status !== "ready" || plan === null || plan.blockers.length !== 0) return null
    const frozen = this.preparedInput
    if (frozen === null || frozen.workspace !== input.workspace || frozen.phase !== input.phase || frozen.csrf !== input.csrf ||
        frozen.request.interface !== input.request.interface || frozen.request.storage_size_gib !== input.request.storage_size_gib) {
      this.reset()
      return null
    }
    if (this.now() >= plan.expiresAt) { await this.prepare(input); return null }
    const revision = this.revision
    clearTimeout(this.expiryTimer)
    this.publish({ ...this.state, status: "applying", errorCode: null })
    let token = ""
    try {
      const proof = await this.api.confirm(frozen, plan.id)
      token = proof.token
      if (revision !== this.revision || this.disposed) return null
      const expiry = Date.parse(proof.expires_at)
      if (!token || !Number.isFinite(expiry) || expiry <= this.now() || this.now() >= plan.expiresAt || expiry > plan.expiresAt + 1) {
        await this.prepare(input)
        return null
      }
      const response = await this.api.apply(frozen, plan.id, token, plan.parameters)
      token = ""
      if (revision !== this.revision || this.disposed) return null
      if (response.job.workspace_id !== input.workspace) throw new APIProblemError({ code: "maintenance_response_invalid" })
      this.publish({ status: "submitted", plan: null, jobID: response.job.id, errorCode: null })
      return response.job.id
    } catch (error) {
      if (revision === this.revision && !this.disposed) this.publish({ status: "error", plan: null, jobID: plan.id, errorCode: error instanceof APIProblemError ? error.code : "request_failed" })
      // An uncertain apply is never retried; the durable job service owns its
      // outcome. Reopening this view must not reuse a consumed approval.
      return null
    } finally { token = "" }
  }
}
