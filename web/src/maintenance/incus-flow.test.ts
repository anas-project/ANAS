import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import type { HostActionJob } from "../api/maintenance"
import { IncusApprovalFlow, readIncusPlan, type IncusFlowAPI, type IncusInput } from "./incus-flow"

const input: IncusInput = { workspace: "main", phase: "install", csrf: "fixture-csrf",
  request: { interface: "incus_container", storage_size_gib: 64 } }
const digest = "a".repeat(64)
function plan(id = "plan-1", status: HostActionJob["job"]["status"] = "succeeded"): HostActionJob {
  return { api_version: "anas.dev/api/v1", job: { id, kind: "action", workspace_id: "main", mutating: false,
    status, progress: 100, revision: 2, warnings: [], needs_compensation_check: false, created_at: new Date().toISOString(),
    result: { changed: false, value: { schema: "anas.host-action.incus/v1", action: "incus.install", phase: "install",
      parameters: { schema: "anas.host-action.incus/v1", request: { ...input.request },
        binding: { schema: "anas.incus-host-provision/v1", phase: "install", destructive: true, plan_digest: digest } },
      inspect: { schema: "anas.incus-host-provision/v1", plan: { schema: "anas.incus-host-provision/v1", digest, blockers: [],
        steps: [{ id: "packages", phase: "install", effect: "Install approved packages", destructive: true }] } },
    } },
  } }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((r) => { resolve = r })
  return { promise, resolve }
}
function fixture() {
  const api: IncusFlowAPI = {
    plan: vi.fn(async () => plan()), get: vi.fn(async () => plan()),
    confirm: vi.fn(async () => ({ token: "private-one-use-token", expires_at: new Date(Date.now() + 300_000).toISOString() })),
    apply: vi.fn(async () => { const response = plan("apply-1", "queued"); response.job.mutating = true; return response }),
  }
  const changed = vi.fn()
  return { api, changed, flow: new IncusApprovalFlow(api, changed) }
}
beforeEach(() => { vi.useFakeTimers(); vi.setSystemTime(new Date("2026-09-19T12:00:00Z")) })
afterEach(() => { vi.clearAllTimers(); vi.useRealTimers() })

describe("Incus approval workflow", () => {
  it("preserves the server's frozen mirror selection through approval", async () => {
    for (const speedup of [true, false]) {
      const { flow, api } = fixture()
      const response = plan()
      const value = response.job.result?.value as { parameters: { request: Record<string, unknown> } }
      value.parameters.request.chinese_speedup = speedup
      vi.mocked(api.plan).mockResolvedValueOnce(response)
      await flow.prepare(input)
      expect(flow.state.plan?.parameters.request.chinese_speedup).toBe(speedup)
      await flow.execute(input, true)
      expect(vi.mocked(api.apply).mock.calls[0]?.[3].request.chinese_speedup).toBe(speedup)
      flow.dispose()
      value.parameters.request.chinese_speedup = "true"
      expect(() => readIncusPlan(response, input, "plan-1")).toThrow()
    }
  })

  it("shows the server plan and applies only after explicit approval without exposing the token", async () => {
    const { flow, api, changed } = fixture()
    await flow.prepare(input)
    expect(flow.state.status).toBe("ready")
    expect(flow.state.plan?.steps[0]?.effect).toBe("Install approved packages")
    await flow.execute(input, false)
    expect(api.confirm).not.toHaveBeenCalled()
    const parameters = flow.state.plan?.parameters
    expect(await flow.execute(input, true)).toBe("apply-1")
    expect(api.apply).toHaveBeenCalledWith(input, "plan-1", "private-one-use-token", parameters)
    expect(JSON.stringify(changed.mock.calls)).not.toContain("private-one-use-token")
    expect(flow.state.status).toBe("submitted")
    flow.dispose()
  })

  it("polls the same durable job and stops polling when the view is disposed", async () => {
    const { flow, api } = fixture()
    vi.mocked(api.plan).mockResolvedValueOnce(plan("plan-1", "queued"))
    const preparing = flow.prepare(input)
    await vi.advanceTimersByTimeAsync(750)
    await preparing
    expect(api.get).toHaveBeenCalledOnce()
    expect(api.get).toHaveBeenCalledWith("plan-1", expect.any(AbortSignal))
    flow.dispose()
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.plan).toHaveBeenCalledOnce()
  })

  it("discards an in-flight confirmation after the workspace or selection changes", async () => {
    const { flow, api } = fixture()
    await flow.prepare(input)
    const pending = deferred<{ token: string; expires_at: string }>()
    vi.mocked(api.confirm).mockReturnValueOnce(pending.promise)
    const execution = flow.execute(input, true)
    flow.reset()
    pending.resolve({ token: "discarded-proof", expires_at: new Date(Date.now() + 300_000).toISOString() })
    expect(await execution).toBeNull()
    expect(api.apply).not.toHaveBeenCalled()
    expect(flow.state.status).toBe("idle")
  })

  it("cannot reuse approval with changed input even without a component watcher", async () => {
    for (const changed of [
      { ...input, workspace: "other" }, { ...input, phase: "uninstall" as const },
      { ...input, request: { ...input.request, storage_size_gib: 128 } }, { ...input, csrf: "changed" },
    ]) {
      const { flow, api } = fixture()
      await flow.prepare(input)
      expect(await flow.execute(changed, true)).toBeNull()
      expect(api.confirm).not.toHaveBeenCalled()
      expect(api.apply).not.toHaveBeenCalled()
      flow.dispose()
    }
  })

  it("refreshes an expired plan without signing or executing the replacement", async () => {
    const { flow, api } = fixture()
    let count = 0
    vi.mocked(api.plan).mockImplementation(async () => plan(`plan-${++count}`))
    await flow.prepare(input)
    await vi.advanceTimersByTimeAsync(300_000)
    expect(flow.state.status).toBe("ready")
    expect(flow.state.plan?.id).toBe("plan-2")
    expect(api.confirm).not.toHaveBeenCalled()
    expect(api.apply).not.toHaveBeenCalled()
    flow.dispose()
  })

  it("requires a fresh review when confirmation expires while being issued", async () => {
    const { flow, api } = fixture()
    await flow.prepare(input)
    vi.mocked(api.confirm).mockResolvedValueOnce({ token: "expired", expires_at: new Date(Date.now() - 1).toISOString() })
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.plan).toHaveBeenCalledTimes(2)
    expect(api.apply).not.toHaveBeenCalled()
    flow.dispose()
  })

  it("does not automatically retry an uncertain apply", async () => {
    const { flow, api } = fixture()
    await flow.prepare(input)
    vi.mocked(api.apply).mockRejectedValueOnce(new Error("private-network-details"))
    expect(await flow.execute(input, true)).toBeNull()
    expect(flow.state.status).toBe("error")
    expect(flow.state.errorCode).toBe("request_failed")
    await vi.advanceTimersByTimeAsync(600_000)
    expect(api.apply).toHaveBeenCalledOnce()
    expect(api.plan).toHaveBeenCalledOnce()
    expect(JSON.stringify(flow.state)).not.toContain("private-network-details")
    flow.dispose()
  })

  it("rejects foreign, drifted and malformed plans", () => {
    for (const mutate of [
      (r: HostActionJob) => { r.job.workspace_id = "other" },
      (r: HostActionJob) => { r.job.mutating = true },
      (r: HostActionJob) => { r.job.created_at = "not-a-date" },
      (r: HostActionJob) => { r.job.result = { value: {} } },
    ]) {
      const response = plan()
      mutate(response)
      expect(() => readIncusPlan(response, input, "plan-1")).toThrow()
    }
  })

  it("does not apply a blocked server plan", async () => {
    const { flow, api } = fixture()
    const response = plan()
    const value = response.job.result?.value as { inspect: { plan: { blockers: string[] } } }
    value.inspect.plan.blockers = ["running_managed_guests"]
    vi.mocked(api.plan).mockResolvedValueOnce(response)
    await flow.prepare(input)
    expect(await flow.execute(input, true)).toBeNull()
    expect(api.confirm).not.toHaveBeenCalled()
    flow.dispose()
  })
})
