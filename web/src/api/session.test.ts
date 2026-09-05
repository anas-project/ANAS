import { afterEach, describe, expect, it, vi } from "vitest"

import { api } from "./client"
import {
  clockSkewMs,
  expiresAt,
  formatCountdown,
  observeSession,
  recordServerClock,
  remainingMs,
  reportSession,
  sessionLifetime,
  slidesIdleWindow,
  withActivity,
  type SessionSignal,
} from "./session"

const receivedAt = Date.parse("2026-09-05T12:00:00Z")
const instants = {
  expires_at: "2026-09-06T00:00:00Z",
  idle_expires_at: "2026-09-05T12:30:00Z",
}

function problemResponse(code: string, status = 401): Response {
  return new Response(JSON.stringify({ api_version: "anas.dev/api/v1", status, code, detail: code }), {
    status,
    headers: { "Content-Type": "application/problem+json", Date: "Sat, 05 Sep 2026 12:00:00 GMT" },
  })
}

function collectSignals(): { signals: SessionSignal[]; stop: () => void } {
  const signals: SessionSignal[] = []
  const stop = observeSession((signal) => signals.push(signal))
  return { signals, stop }
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
  recordServerClock("Sat, 05 Sep 2026 12:00:00 GMT", Date.parse("2026-09-05T12:00:00Z"))
})

describe("session lifetime", () => {
  it("expires at the earlier of the absolute deadline and the idle window", () => {
    const lifetime = sessionLifetime(instants, 0, receivedAt)
    expect(lifetime).not.toBeNull()
    expect(lifetime?.idleWindowMs).toBe(30 * 60 * 1000)
    expect(expiresAt(lifetime!)).toBe(Date.parse("2026-09-05T12:30:00Z"))
    expect(remainingMs(lifetime!, receivedAt)).toBe(30 * 60 * 1000)
  })

  it("translates server instants onto a browser clock that disagrees", () => {
    // The browser reads 12:00 while the daemon is already at 12:10, so the
    // 12:30 idle deadline is twenty minutes away, not the thirty a naive
    // comparison against the local clock would show.
    const skewMs = 10 * 60 * 1000
    const lifetime = sessionLifetime(instants, skewMs, receivedAt)
    expect(remainingMs(lifetime!, receivedAt)).toBe(20 * 60 * 1000)
    expect(lifetime?.absoluteExpiresAt).toBe(Date.parse("2026-09-06T00:00:00Z") - skewMs)
    expect(remainingMs(lifetime!, receivedAt + 20 * 60 * 1000)).toBe(0)
  })

  it("slides the idle window on activity but never past the absolute deadline", () => {
    const lifetime = sessionLifetime(instants, 0, receivedAt)!
    const later = withActivity(lifetime, receivedAt + 20 * 60 * 1000)
    expect(expiresAt(later)).toBe(Date.parse("2026-09-05T12:50:00Z"))
    expect(withActivity(later, receivedAt).lastActivityAt).toBe(later.lastActivityAt)

    const nearAbsolute = withActivity(lifetime, Date.parse("2026-09-05T23:50:00Z"))
    expect(expiresAt(nearAbsolute)).toBe(Date.parse("2026-09-06T00:00:00Z"))
  })

  it("falls back to the absolute deadline when no usable idle deadline is reported", () => {
    const lifetime = sessionLifetime(
      { ...instants, idle_expires_at: "0001-01-01T00:00:00Z" },
      0,
      receivedAt,
    )
    expect(lifetime?.idleWindowMs).toBe(Number.POSITIVE_INFINITY)
    expect(expiresAt(lifetime!)).toBe(Date.parse("2026-09-06T00:00:00Z"))
  })

  it("rejects a response without a parsable absolute deadline", () => {
    expect(sessionLifetime({ ...instants, expires_at: "not-a-time" }, 0, receivedAt)).toBeNull()
  })

  it("never reports negative time and formats minutes and hours", () => {
    const lifetime = sessionLifetime(instants, 0, receivedAt)!
    expect(remainingMs(lifetime, receivedAt + 60 * 60 * 1000)).toBe(0)
    expect(formatCountdown(0)).toBe("0:00")
    expect(formatCountdown(65_000)).toBe("1:05")
    expect(formatCountdown(3_725_000)).toBe("1:02:05")
  })

  it("keeps public routes from sliding the local idle estimate", () => {
    expect(slidesIdleWindow("/api/v1/system")).toBe(false)
    expect(slidesIdleWindow("/api/v1/system/ca")).toBe(false)
    expect(slidesIdleWindow("/healthz")).toBe(false)
    expect(slidesIdleWindow("/api/v1/auth/csrf")).toBe(false)
    expect(slidesIdleWindow("/api/v1/jobs")).toBe(true)
    expect(slidesIdleWindow("/api/v1/auth/session")).toBe(true)
  })

  it("keeps the last parsable server clock and ignores unusable headers", () => {
    recordServerClock("Sat, 05 Sep 2026 12:01:00 GMT", Date.parse("2026-09-05T12:00:00Z"))
    expect(clockSkewMs()).toBe(60_000)
    recordServerClock(null, Date.parse("2026-09-05T12:00:00Z"))
    recordServerClock("not-a-date", Date.parse("2026-09-05T12:00:00Z"))
    expect(clockSkewMs()).toBe(60_000)
  })

  it("stops delivering signals after an observer unsubscribes", () => {
    const { signals, stop } = collectSignals()
    reportSession({ kind: "unauthenticated" })
    stop()
    reportSession({ kind: "unauthenticated" })
    expect(signals).toEqual([{ kind: "unauthenticated" }])
  })
})

describe("api client session signals", () => {
  it("reports an expired session once, from any route", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(problemResponse("unauthenticated")))
    const { signals, stop } = collectSignals()

    await api.GET("/api/v1/jobs")

    stop()
    expect(signals).toEqual([{ kind: "unauthenticated" }])
  })

  it("leaves a rejected credential to the caller instead of ending the session", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(problemResponse("invalid_credentials")))
    const { signals, stop } = collectSignals()

    await api.GET("/api/v1/jobs")

    stop()
    expect(signals).toEqual([])
  })

  it("records activity for authenticated routes only", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(
        async () =>
          new Response(JSON.stringify({ api_version: "anas.dev/api/v1", items: [] }), {
            status: 200,
            headers: { "Content-Type": "application/json", Date: "Sat, 05 Sep 2026 12:00:00 GMT" },
          }),
      ),
    )
    const { signals, stop } = collectSignals()

    await api.GET("/api/v1/jobs")
    await api.GET("/api/v1/system")

    stop()
    expect(signals).toHaveLength(1)
    expect(signals[0]?.kind).toBe("activity")
  })

  it("keeps the response body readable by the caller after inspecting a 401", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(problemResponse("unauthenticated")))

    const { error } = await api.GET("/api/v1/jobs")

    expect(error).toMatchObject({ code: "unauthenticated" })
  })
})
