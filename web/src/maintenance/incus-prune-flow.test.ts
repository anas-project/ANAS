import { afterEach, describe, expect, it, vi } from "vitest"
import { IncusPruneFlow, readPrunePlan, type PruneAPI, type PruneInput } from "./incus-prune-flow"
import type { HostActionJob } from "../api/maintenance"

const input: PruneInput = { workspace: "main", csrf: "csrf" }
const now = Date.parse("2026-09-19T12:00:00Z")
const target = { project: "project", fingerprint: "a".repeat(64) }
function planValue(workspace = "main", blockers: string[] = []) {
  const digest = "b".repeat(64), stateDigest = "c".repeat(64)
  return {
    schema: "anas.host-action.incus/v1", action: "incus.image-prune",
    plan: { schema: "anas.incus-image-prune/v1", workspace_id: workspace, digest, state_digest: stateDigest,
      delete: [target] as typeof target[] | null, retained: [] as (typeof target & { reasons: string[] })[] | null,
      summary: { delete: 1, retained: 0 }, blockers },
    parameters: { schema: "anas.host-action.incus/v1", request: { schema: "anas.incus-image-prune/v1", workspace_id: workspace },
      binding: { schema: "anas.incus-image-prune/v1", workspace_id: workspace, plan_digest: digest, state_digest: stateDigest,
        summary_digest: digest, delete: [target] as typeof target[] | null } },
  }
}
function plan(id = "plan", workspace = "main", blockers: string[] = []): HostActionJob {
  return { api_version: "anas.dev/api/v1", job: {
    id, kind: "action", workspace_id: workspace, mutating: false, status: "succeeded", progress: 100,
    created_at: new Date(now).toISOString(), revision: 1, warnings: [], needs_compensation_check: false,
    result: { changed: false, value: planValue(workspace, blockers) },
  } }
}
function applied(): HostActionJob {
  return { api_version: "anas.dev/api/v1", job: {
    id: "apply", kind: "action", workspace_id: "main", mutating: true, status: "queued", progress: 0,
    created_at: new Date(now).toISOString(), revision: 1, warnings: [], needs_compensation_check: false,
  } }
}
function fixture() {
  let time = now
  const api: PruneAPI = {
    plan: vi.fn(async () => plan()), get: vi.fn(async () => plan()),
    confirm: vi.fn(async () => ({ token: "memory-only", expires_at: new Date(now + 300000).toISOString() })),
    apply: vi.fn(async () => applied()),
  }
  const flow = new IncusPruneFlow(api, vi.fn(), () => time)
  flows.push(flow)
  return { api, flow, clock: (value: number) => { time = value } }
}
const flows: IncusPruneFlow[] = []
afterEach(() => { for (const flow of flows) flow.dispose(); flows.length = 0; vi.useRealTimers() })

describe("Incus image cleanup approval", () => {
  it("requires explicit approval and never keeps the token in public state", async () => {
    const { api, flow } = fixture()
    await flow.prepare(input)
    expect(await flow.execute(input, false)).toBeNull()
    expect(api.confirm).not.toHaveBeenCalled()
    expect(await flow.execute(input, true)).toBe("apply")
    expect(api.apply).toHaveBeenCalledExactlyOnceWith(input, "plan", "memory-only")
    expect(JSON.stringify(flow.state)).not.toContain("memory-only")
    expect(await flow.execute(input, true)).toBeNull()
  })
  it("refreshes expired plans without applying or retaining approval", async () => {
    const { api, flow, clock } = fixture()
    await flow.prepare(input); clock(now + 300001)
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.plan).toHaveBeenCalledTimes(2)
    expect(api.confirm).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })
  it("rejects stale workspace responses and aborts polling on reset", async () => {
    const { api, flow } = fixture()
    let resolve!: (value: HostActionJob) => void
    let signal: AbortSignal | undefined
    api.get = vi.fn((_id, value) => { signal = value; return new Promise<HostActionJob>(done => { resolve = done }) })
    const pending = flow.prepare(input)
    await Promise.resolve(); flow.reset(); resolve(plan())
    await pending
    expect(signal?.aborted).toBe(true)
    expect(flow.state.status).toBe("idle")
    expect(api.apply).not.toHaveBeenCalled()
  })
  it("refuses blockers, response identity drift and fake inventory counts", async () => {
    const { api, flow } = fixture()
    api.get = vi.fn(async () => plan("plan", "main", ["stop_compute_consumers_before_image_prune"]))
    await flow.prepare(input)
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.confirm).not.toHaveBeenCalled()
    expect(() => readPrunePlan(plan("plan", "other"), input, "plan", now)).toThrow()
    const invalid = plan()
    const value = planValue(); value.plan.summary.delete = 99
    invalid.job.result = { changed: false, value }
    expect(() => readPrunePlan(invalid, input, "plan", now)).toThrow()
  })
  it("never auto-retries an uncertain apply, and stops after losing its owner", async () => {
    const { api, flow } = fixture()
    await flow.prepare(input)
    expect(await flow.execute({ ...input, workspace: "other" }, true)).toBeNull()
    expect(api.confirm).not.toHaveBeenCalled()
    await flow.prepare(input)
    api.apply = vi.fn(async () => { throw { code: "host_actions_unavailable" } })
    await flow.execute(input, true)
    expect(flow.state.status).toBe("failed")
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.apply).toHaveBeenCalledTimes(1)
  })

  it("accepts the real public job envelope without exposing internal action state", () => {
    const response = plan()
    expect(response.job).not.toHaveProperty("action")
    expect(readPrunePlan(response, input, "plan", now).delete).toEqual([{ ...target, reasons: [] }])
  })

  it("shows a valid no-change plan when Go serializes empty slices as null", async () => {
    const { api, flow } = fixture()
    const response = plan(), value = planValue()
    value.plan.delete = null
    value.plan.retained = null
    value.plan.summary.delete = 0
    value.parameters.binding.delete = null
    response.job.result = { changed: false, value }
    api.get = vi.fn(async () => response)
    await flow.prepare(input)
    expect(flow.state.status).toBe("ready")
    expect(flow.state.plan?.delete).toEqual([])
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.confirm).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
  })

  it("rejects mismatched action results, bindings, and mutating plan jobs", () => {
    const invalid = plan()
    invalid.job.mutating = true
    expect(() => readPrunePlan(invalid, input, "plan", now)).toThrow()
    for (const change of [
      (value: ReturnType<typeof planValue>) => { value.action = "incus.uninstall" },
      (value: ReturnType<typeof planValue>) => { value.parameters.binding.workspace_id = "other" },
      (value: ReturnType<typeof planValue>) => { value.parameters.binding.state_digest = "d".repeat(64) },
      (value: ReturnType<typeof planValue>) => { value.parameters.binding.plan_digest = "d".repeat(64) },
      (value: ReturnType<typeof planValue>) => { value.parameters.binding.delete = [{ ...target, fingerprint: "d".repeat(64) }] },
    ]) {
      const response = plan(), value = planValue()
      change(value)
      response.job.result = { changed: false, value }
      expect(() => readPrunePlan(response, input, "plan", now)).toThrow()
    }
  })

  it("freezes the input while the plan request is in flight", async () => {
    const { api, flow } = fixture()
    let finish!: (value: HostActionJob) => void
    api.plan = vi.fn(() => new Promise<HostActionJob>(resolve => { finish = resolve }))
    const changing = { ...input }
    const preparing = flow.prepare(changing)
    changing.workspace = "other"
    changing.csrf = "different"
    finish(plan())
    await preparing
    expect(api.plan).toHaveBeenCalledExactlyOnceWith(input)
    expect(flow.state.status).toBe("ready")
    expect(await flow.execute(changing, true)).toBeNull()
    expect(api.apply).not.toHaveBeenCalled()
  })

  it("discards a pending confirmation on disposal and never applies it", async () => {
    const { api, flow } = fixture()
    await flow.prepare(input)
    let finish!: (proof: { token: string; expires_at: string }) => void
    api.confirm = vi.fn(() => new Promise<{ token: string; expires_at: string }>(resolve => { finish = resolve }))
    const applying = flow.execute(input, true)
    flow.dispose()
    finish({ token: "discarded-token", expires_at: new Date(now + 300000).toISOString() })
    expect(await applying).toBeNull()
    expect(api.apply).not.toHaveBeenCalled()
    expect(JSON.stringify(flow.state)).not.toContain("discarded-token")
  })
})
