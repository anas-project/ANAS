import { afterEach, describe, expect, it, vi } from "vitest"

import { getWorkspaceConfig, putWorkspaceConfig, validateWorkspaceConfig } from "./config"

const validator = `cfgv-${"a".repeat(64)}`

function response(body: unknown, headers: HeadersInit = {}): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json", ...headers },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("configuration API", () => {
  it("captures the strong ETag returned with a managed snapshot", async () => {
    const fetch = vi.fn().mockResolvedValue(
      response(
        {
          api_version: "anas.dev/api/v1",
          workspace_id: "main",
          managed: true,
          config: {},
          available_modules: [],
          fields: [],
        },
        { ETag: `"${validator}"` },
      ),
    )
    vi.stubGlobal("fetch", fetch)
    const result = await getWorkspaceConfig("main")
    expect(result.etag).toBe(`"${validator}"`)
    expect(fetch).toHaveBeenCalledOnce()
  })

  it("sends same-origin CSRF for validation", async () => {
    const fetch = vi.fn().mockResolvedValue(
      response({ api_version: "anas.dev/api/v1", workspace_id: "main", config: {}, changes: [] }),
    )
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await validateWorkspaceConfig("main", { config: {} }, "csrf-value")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Origin")).toBe("https://nas.example")
    expect(request.headers.get("X-CSRF-Token")).toBe("csrf-value")
  })

  it("uses If-Match for a managed save and If-None-Match for the first save", async () => {
    const fetch = vi.fn().mockImplementation(async () =>
      response({
        api_version: "anas.dev/api/v1",
        workspace_id: "main",
        validator,
        config: {},
        available_modules: [],
        fields: [],
        changes: [],
      }),
    )
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)

    await putWorkspaceConfig("main", { config: {} }, "csrf", true, `"${validator}"`)
    let request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("If-Match")).toBe(`"${validator}"`)
    expect(request.headers.has("If-None-Match")).toBe(false)

    await putWorkspaceConfig("main", { config: {} }, "csrf", false, null)
    request = fetch.mock.calls[1]?.[0] as Request
    expect(request.headers.get("If-None-Match")).toBe("*")
    expect(request.headers.has("If-Match")).toBe(false)
  })
})
