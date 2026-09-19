import type { HostActionJob } from "../api/maintenance"

export type PruneInput = { workspace: string; csrf: string }
export type PruneTarget = { project: string; fingerprint: string }
export type PrunePlan = { id: string; expiresAt: number; delete: PruneTarget[]; retained: (PruneTarget & { reasons: string[] })[]; blockers: string[] }
export type PruneState = { status: "idle" | "planning" | "ready" | "applying" | "submitted" | "failed"; plan: PrunePlan | null; errorCode: string | null; jobID: string }
export interface PruneAPI {
  plan(input: PruneInput): Promise<HostActionJob>
  get(id: string, signal: AbortSignal): Promise<HostActionJob>
  confirm(input: PruneInput, id: string): Promise<{ token: string; expires_at: string }>
  apply(input: PruneInput, id: string, token: string): Promise<HostActionJob>
}
const object = (value: unknown): Record<string, unknown> => typeof value === "object" && value !== null && !Array.isArray(value) ? value as Record<string, unknown> : {}
const fingerprint = /^[0-9a-f]{64}$/
function labels(value: unknown): string[] {
  if (value === undefined) return []
  if (!Array.isArray(value) || value.length > 16 || !value.every(v => typeof v === "string" && /^[a-z_]{1,80}$/.test(v))) throw new Error("plan_response_invalid")
  return value as string[]
}
function targets(value: unknown, retained: boolean): (PruneTarget & { reasons: string[] })[] {
  if (value === null) return [] // Go encodes an empty, nil inventory slice as null.
  if (!Array.isArray(value) || value.length > (retained ? 512 : 128)) throw new Error("plan_response_invalid")
  const seen = new Set<string>()
  return value.map(item => {
    const entry = object(item)
    if (typeof entry.project !== "string" || !/^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$/.test(entry.project) || entry.project === "default" ||
        typeof entry.fingerprint !== "string" || !fingerprint.test(entry.fingerprint)) throw new Error("plan_response_invalid")
    const key = `${entry.project}/${entry.fingerprint}`
    if (seen.has(key)) throw new Error("plan_response_invalid")
    seen.add(key)
    return { project: entry.project, fingerprint: entry.fingerprint, reasons: retained ? labels(entry.reasons) : [] }
  })
}
export function readPrunePlan(response: HostActionJob, input: PruneInput, id: string, now: number): PrunePlan {
  const job = response.job
  const value = object(job.result?.value), plan = object(value.plan), summary = object(plan.summary)
  const expiresAt = Date.parse(job.created_at) + 300000
  if (job.id !== id || job.workspace_id !== input.workspace || job.status !== "succeeded" || job.kind !== "action" || job.mutating ||
      response.api_version !== "anas.dev/api/v1" || job.result?.changed !== false || value.schema !== "anas.host-action.incus/v1" ||
      value.action !== "incus.image-prune" || plan.schema !== "anas.incus-image-prune/v1" || plan.workspace_id !== input.workspace ||
      typeof plan.digest !== "string" || !fingerprint.test(plan.digest) || typeof plan.state_digest !== "string" || !fingerprint.test(plan.state_digest) || !Number.isFinite(expiresAt) || expiresAt <= now) throw new Error("plan_response_invalid")
  const remove = targets(plan.delete, false), retained = targets(plan.retained, true)
  if (summary.delete !== remove.length || summary.retained !== retained.length || remove.some(t => retained.some(r => r.project === t.project && r.fingerprint === t.fingerprint))) throw new Error("plan_response_invalid")
  const parameters = object(value.parameters), request = object(parameters.request), binding = object(parameters.binding)
  if (parameters.schema !== value.schema || request.schema !== plan.schema || binding.schema !== plan.schema ||
      request.workspace_id !== input.workspace || binding.workspace_id !== input.workspace ||
      binding.plan_digest !== plan.digest || binding.summary_digest !== plan.digest || binding.state_digest !== plan.state_digest) throw new Error("plan_response_invalid")
  const approved = targets(binding.delete, false)
  if (approved.length !== remove.length || approved.some(t => !remove.some(r => r.project === t.project && r.fingerprint === t.fingerprint))) throw new Error("plan_response_invalid")
  return { id, expiresAt, delete: remove, retained, blockers: labels(plan.blockers) }
}
const errorCode = (error: unknown): string => {
  const code = object(error).code
  return typeof code === "string" ? code : error instanceof Error && error.message === "plan_response_invalid" ? error.message : "host_actions_unavailable"
}

export class IncusPruneFlow {
  state: PruneState = { status: "idle", plan: null, errorCode: null, jobID: "" }
  private serial = 0
  private abort: AbortController | undefined
  private refresh: ReturnType<typeof setTimeout> | undefined
  private disposed = false
  private owner: PruneInput | null = null
  constructor(private api: PruneAPI, private changed: (state: PruneState) => void, private now = Date.now) {}
  reset(): void {
    ++this.serial; this.abort?.abort(); this.abort = undefined
    if (this.refresh !== undefined) clearTimeout(this.refresh)
    this.refresh = undefined
    this.owner = null
    this.publish({ status: "idle", plan: null, errorCode: null, jobID: "" })
  }
  dispose(): void { this.reset(); this.disposed = true }
  private publish(value: PruneState): void { this.state = value; if (!this.disposed) this.changed(value) }
  async prepare(input: PruneInput): Promise<void> {
    if (this.disposed) return
    this.reset()
    const frozen = { ...input }
    this.owner = frozen
    const serial = this.serial, abort = new AbortController()
    this.abort = abort
    this.publish({ status: "planning", plan: null, errorCode: null, jobID: "" })
    try {
      const created = await this.api.plan(frozen)
      if (serial !== this.serial || abort.signal.aborted) return
      const id = created.job.id
      if (!id || created.api_version !== "anas.dev/api/v1" || created.job.workspace_id !== frozen.workspace || created.job.kind !== "action" || created.job.mutating) throw new Error("plan_response_invalid")
      const deadline = this.now() + 90000
      while (serial === this.serial && !abort.signal.aborted) {
        const response = await this.api.get(id, abort.signal)
        if (serial !== this.serial || abort.signal.aborted) return
        if (response.job.id !== id || response.job.workspace_id !== frozen.workspace) throw new Error("plan_response_invalid")
        if (response.job.status === "succeeded") {
          const plan = readPrunePlan(response, frozen, id, this.now())
          this.publish({ status: "ready", plan, errorCode: null, jobID: id })
          this.refresh = setTimeout(() => { if (serial === this.serial) void this.prepare(frozen) }, Math.max(1, plan.expiresAt - this.now() - 15000))
          return
        }
        if (["failed", "canceled", "interrupted"].includes(response.job.status)) throw { code: response.job.error?.code ?? "host_actions_unavailable" }
        if (this.now() >= deadline) throw { code: "deadline_exceeded" }
        await new Promise<void>(resolve => {
          const done = () => { clearTimeout(timer); abort.signal.removeEventListener("abort", done); resolve() }
          const timer = setTimeout(done, 400)
          abort.signal.addEventListener("abort", done, { once: true })
        })
      }
    } catch (error) {
      if (serial === this.serial && !abort.signal.aborted) this.publish({ status: "failed", plan: null, errorCode: errorCode(error), jobID: "" })
    }
  }
  async execute(input: PruneInput, approved: boolean): Promise<string | null> {
    if (this.disposed) return null
    if (this.owner?.workspace !== input.workspace || this.owner?.csrf !== input.csrf) { this.reset(); return null }
    const frozen = this.owner
    const plan = this.state.plan
    if (!frozen || !approved || this.state.status !== "ready" || !plan || plan.blockers.length || !plan.delete.length) return null
    if (plan.expiresAt <= this.now() + 2000) { await this.prepare(frozen); return null }
    if (this.refresh !== undefined) clearTimeout(this.refresh)
    const serial = this.serial
    this.publish({ ...this.state, status: "applying", errorCode: null })
    try {
      const proof = await this.api.confirm(frozen, plan.id)
      if (serial !== this.serial) return null
      const expiresAt = Date.parse(proof.expires_at)
      if (!proof.token || !Number.isFinite(expiresAt) || expiresAt <= this.now() || expiresAt > plan.expiresAt) { await this.prepare(frozen); return null }
      const result = await this.api.apply(frozen, plan.id, proof.token)
      if (serial !== this.serial) return null
      if (result.api_version !== "anas.dev/api/v1" || result.job.workspace_id !== frozen.workspace || result.job.kind !== "action" || !result.job.mutating || !result.job.id) throw new Error("plan_response_invalid")
      this.publish({ status: "submitted", plan: null, errorCode: null, jobID: result.job.id })
      return result.job.id
    } catch (error) {
      if (serial !== this.serial) return null
      const code = errorCode(error)
      if (["confirmation_invalid", "confirmation_consumed", "confirmation_expired"].includes(code)) { await this.prepare(frozen); return null }
      this.publish({ status: "failed", plan: null, errorCode: code, jobID: "" })
      return null
    }
  }
}
