import { afterEach, describe, expect, it, vi } from "vitest"

import {
  applyWorkspaceDeployment,
  issueDeploymentStepUp,
  listJobs,
  newIdempotencyKey,
  planWorkspaceDeployment,
} from "./deployment"

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("deployment API", () => {
  it("binds full-state step-up and plan to the same workspace and origin", async () => {
    const fetch = vi
      .fn()
      .mockResolvedValueOnce(
        response({
          api_version: "anas.dev/api/v1",
          proof: `sup_${"A".repeat(43)}`,
          expires_at: "2026-09-01T00:05:00Z",
          action: "deployment.apply",
          workspace_id: "main",
        }),
      )
      .mockResolvedValueOnce(
        response({
          api_version: "anas.dev/api/v1",
          workspace_id: "main",
          job: {},
          plan: {},
          confirmation: {},
        }),
      )
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)

    const stepUp = await issueDeploymentStepUp("main", "owner-password", "csrf")
    await planWorkspaceDeployment("main", "csrf", stepUp.proof)

    const stepRequest = fetch.mock.calls[0]?.[0] as Request
    expect(stepRequest.headers.get("Origin")).toBe("https://nas.example")
    expect(stepRequest.headers.get("X-CSRF-Token")).toBe("csrf")
    expect(await stepRequest.clone().json()).toEqual({
      password: "owner-password",
      action: "deployment.apply",
      workspace_id: "main",
    })
    const planRequest = fetch.mock.calls[1]?.[0] as Request
    expect(planRequest.headers.get("X-CSRF-Token")).toBe("csrf")
    expect(await planRequest.clone().json()).toEqual({ step_up_proof: stepUp.proof })
  })

  it("sends the explicit risk decision, confirmation binding, and idempotency key", async () => {
    const fetch = vi.fn().mockResolvedValue(
      response(
        {
          api_version: "anas.dev/api/v1",
          job: {
            id: "job-a",
            kind: "deployment.apply",
            workspace_id: "main",
            mutating: true,
            status: "queued",
            progress: 0,
            created_at: "2026-09-01T00:00:00Z",
            revision: 1,
          },
          existing: false,
        },
        202,
      ),
    )
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await applyWorkspaceDeployment(
      "main",
      {
        plan_job_id: "plan-a",
        confirmation_token: `cnf_${"a".repeat(64)}`,
        expected_config_validator: `cfgv-${"b".repeat(64)}`,
        expected_plan_digest: "c".repeat(64),
        build: false,
        update_lock: false,
        allow_risky: true,
        snapshot: false,
        no_snapshot: false,
      },
      "csrf",
      "web-fixed-key",
    )
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Idempotency-Key")).toBe("web-fixed-key")
    expect(request.headers.get("X-CSRF-Token")).toBe("csrf")
    expect(await request.clone().json()).toMatchObject({ allow_risky: true, plan_job_id: "plan-a" })
  })

  it("generates opaque browser idempotency keys", () => {
    const first = newIdempotencyKey()
    const second = newIdempotencyKey()
    expect(first).toMatch(/^web-[0-9a-f]{32}$/)
    expect(second).not.toBe(first)
  })

  it("restores durable jobs from the paginated job API", async () => {
    const fetch = vi.fn().mockResolvedValue(
      response({
        api_version: "anas.dev/api/v1",
        items: [],
        next_cursor: null,
      }),
    )
    vi.stubGlobal("fetch", fetch)

    await listJobs(25, "next-page")

    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.method).toBe("GET")
    expect(new URL(request.url).searchParams).toEqual(new URLSearchParams({ limit: "25", cursor: "next-page" }))
  })
})
