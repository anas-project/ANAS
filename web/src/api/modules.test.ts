import { afterEach, describe, expect, it, vi } from "vitest"

import { configureWorkspaceModule, getWorkspaceModules, updateWorkspaceModules } from "./modules"

const validator = `cfgv-${"a".repeat(64)}`

function response(body: unknown, status = 200, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  })
}

afterEach(() => vi.unstubAllGlobals())

describe("Module management API", () => {
  it("captures the configuration ETag from the combined Module state", async () => {
    const fetch = vi.fn().mockResolvedValue(response({
      api_version: "anas.dev/api/v1", workspace_id: "main", active_deployment: null, modules: [],
    }, 200, { ETag: `"${validator}"` }))
    vi.stubGlobal("fetch", fetch)
    const result = await getWorkspaceModules("main")
    expect(result.etag).toBe(`"${validator}"`)
  })

  it("queues source updates with CSRF and idempotency", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ api_version: "anas.dev/api/v1", job: { id: "job-update" }, existing: false }, 202))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await updateWorkspaceModules("main", { mode: "update", modules: ["demo"] }, "csrf", "module-update")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Origin")).toBe("https://nas.example")
    expect(request.headers.get("X-CSRF-Token")).toBe("csrf")
    expect(request.headers.get("Idempotency-Key")).toBe("module-update")
    expect(await request.clone().json()).toEqual({ mode: "update", modules: ["demo"] })
  })

  it("binds enable to the strong config ETag and an empty body", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ api_version: "anas.dev/api/v1", job: { id: "job-enable" }, existing: false }, 202))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await configureWorkspaceModule("main", "demo", "enable", `"${validator}"`, "csrf", "module-enable")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("If-Match")).toBe(`"${validator}"`)
    expect(request.headers.get("Idempotency-Key")).toBe("module-enable")
    expect(await request.clone().json()).toEqual({})
  })
})
