import { afterEach, describe, expect, it, vi } from "vitest"

import {
  createSnapshot,
  issueLocalAdminRevealStepUp,
  previewTerminalAction,
  rotateLocalAdmin,
} from "./maintenance"

function response(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } })
}

afterEach(() => vi.unstubAllGlobals())

describe("maintenance API", () => {
  it("queues snapshot creation with explicit userdata policy and idempotency", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ api_version: "anas.dev/api/v1", job: { id: "job-snapshot" }, existing: false }, 202))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await createSnapshot("main", "before upgrade", false, "csrf", "snapshot-1")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.headers.get("Idempotency-Key")).toBe("snapshot-1")
    expect(await request.clone().json()).toEqual({ label: "before upgrade", include_userdata: false })
  })

  it("rotates only with an empty body", async () => {
    const fetch = vi.fn().mockResolvedValue(response({ api_version: "anas.dev/api/v1", job: { id: "job-rotate" }, existing: false }, 202))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await rotateLocalAdmin("main", { target_id: `lad_${"a".repeat(64)}`, module: "demo", account: "primary", purpose: "break_glass", username: "admin", url: "" }, "csrf", "rotate-1")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(await request.clone().json()).toEqual({})
  })

  it("binds reveal step-up to the server-provided target ID", async () => {
    const targetID = `lad_${"b".repeat(64)}`
    const fetch = vi.fn().mockResolvedValue(response({ api_version: "anas.dev/api/v1", proof: `sup_${"a".repeat(43)}`, expires_at: "2026-09-03T01:00:00Z", action: "local_admin.reveal", workspace_id: "main", target_id: targetID }))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await issueLocalAdminRevealStepUp("main", targetID, "owner-password", "csrf")
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(await request.clone().json()).toEqual({ password: "owner-password", action: "local_admin.reveal", workspace_id: "main", target_id: targetID })
  })

  it("renders the server descriptor unchanged instead of assembling a command", async () => {
    const descriptor = {
      api_version: "anas.dev/api/v1", workspace_id: "main", operation: "snapshot.restore",
      target: { snapshot_id: "snap-1" }, impact: { data: true, userdata: false, reversible: true },
      argv: ["anas", "snapshot", "restore", "snap-1", "-w", "/registered/main"],
      display: "anas snapshot restore snap-1 -w /registered/main", cli_contract: "docs/reference/contracts/snapshot.md",
    }
    const fetch = vi.fn().mockResolvedValue(response(descriptor))
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    vi.stubGlobal("fetch", fetch)
    await expect(previewTerminalAction("main", { operation: "snapshot.restore", snapshot: { id: "snap-1", restore_userdata: false } }, "csrf")).resolves.toEqual(descriptor)
  })
})
