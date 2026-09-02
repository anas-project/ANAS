import { afterEach, describe, expect, it, vi } from "vitest"

import { executeModuleLifecycle, previewModuleLifecycle } from "./lifecycle"

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("lifecycle API", () => {
  it("previews targets without submitting a client-expanded chain", async () => {
    const fetch = vi.fn().mockResolvedValue(response({
      api_version: "anas.dev/api/v1",
      workspace_id: "main",
      preview: {
        deployment_id: "dep-active",
        action: "restart",
        requested_modules: ["db"],
        affected_modules: ["db", "app"],
        digest: "a".repeat(64),
      },
    }))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)

    await previewModuleLifecycle("main", "restart", ["db"], "csrf")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Idempotency-Key")).toBeNull()
    expect(await request.clone().json()).toEqual({ modules: ["db"] })
  })

  it("submits the exact returned chain with an idempotency key", async () => {
    const fetch = vi.fn().mockResolvedValue(response({
      api_version: "anas.dev/api/v1",
      job: { id: "job-lifecycle" },
      existing: false,
    }, 202))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)

    await executeModuleLifecycle("main", "restart", {
      modules: ["db"],
      expected_deployment_id: "dep-active",
      expected_digest: "a".repeat(64),
      confirmed_modules: ["db", "app"],
    }, "csrf", "web-lifecycle")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Idempotency-Key")).toBe("web-lifecycle")
    expect(request.headers.get("X-CSRF-Token")).toBe("csrf")
    expect(await request.clone().json()).toMatchObject({ confirmed_modules: ["db", "app"] })
  })
})
