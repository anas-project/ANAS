import { afterEach, describe, expect, it, vi } from "vitest"

import { readCookie, refreshAuthSession, validateEnrollmentHandoff } from "./auth"

const validHandoff = {
  api_version: "anas.dev/api/v1" as const,
  handoff: "one-use-value",
  target_origin: "https://nas.example",
  form_action: "https://nas.example/api/v1/auth/enrollment/exchange",
  expires_at: "2026-09-01T00:00:00Z",
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe("authentication helpers", () => {
  it("reads only the named same-origin cookie", () => {
    expect(readCookie("csrf", "session=hidden; csrf=url%2Dsafe; other=value")).toBe("url-safe")
    expect(readCookie("missing", "session=hidden")).toBeNull()
  })

  it("accepts only the fixed exchange path at the declared HTTPS target", () => {
    expect(validateEnrollmentHandoff(validHandoff).href).toBe(
      "https://nas.example/api/v1/auth/enrollment/exchange",
    )
    expect(() =>
      validateEnrollmentHandoff({ ...validHandoff, form_action: "https://attacker.example/collect" }),
    ).toThrow("enrollment_target_invalid")
    expect(() =>
      validateEnrollmentHandoff({ ...validHandoff, target_origin: "http://nas.example" }),
    ).toThrow("enrollment_target_invalid")
    expect(() => validateEnrollmentHandoff({ ...validHandoff, target_origin: "://invalid" })).toThrow(
      "enrollment_target_invalid",
    )
    expect(() =>
      validateEnrollmentHandoff({ ...validHandoff, form_action: `${validHandoff.form_action}?handoff=leak` }),
    ).toThrow("enrollment_target_invalid")
  })

  it("recovers CSRF from the HttpOnly session without sending a stored secret", async () => {
    const fetch = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          api_version: "anas.dev/api/v1",
          csrf_token: "rotated-csrf",
          expires_at: "2026-09-01T01:00:00Z",
          idle_expires_at: "2026-09-01T00:30:00Z",
          state: "full",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    )
    vi.stubGlobal("fetch", fetch)

    await expect(refreshAuthSession()).resolves.toMatchObject({ state: "full", csrf_token: "rotated-csrf" })
    const request = fetch.mock.calls[0]?.[0] as Request
    expect(request.method).toBe("GET")
    expect(request.url).toMatch(/\/api\/v1\/auth\/session$/)
    expect(request.headers.has("Authorization")).toBe(false)
    expect(request.headers.has("X-CSRF-Token")).toBe(false)
  })
})
