// REQUIREMENTS: CONSOLE-R-116 CONSOLE-R-185 CONSOLE-R-186
// The session token is an HttpOnly cookie the SPA can never read, so the
// remaining lifetime is tracked from what the server does report: an absolute
// deadline that cannot be extended and an idle window that every authenticated
// request slides forward (R-116). Both arrive as server-clock timestamps, and a
// browser clock minutes away from the daemon's would otherwise produce a
// countdown that is confidently wrong, so each response's `Date` header
// translates them onto the local clock once, at receipt.
//
// The idle window is deliberately estimated locally rather than polled: every
// authenticated request — including a poll of the session route — slides the
// server-side window, so a countdown that refreshed itself over the network
// would keep the session alive forever and silently turn a 30-minute idle
// timeout into the 12-hour absolute one.

export interface SessionInstants {
  expires_at: string
  idle_expires_at: string
}

export interface SessionLifetime {
  /** Local-clock instant the absolute lifetime ends; never extended. */
  absoluteExpiresAt: number
  /** Idle allowance as a duration, so it survives the clock translation. */
  idleWindowMs: number
  /** Local-clock instant of the last request known to have slid the window. */
  lastActivityAt: number
}

export type SessionSignal = { kind: "unauthenticated" } | { kind: "activity"; at: number }

type SessionObserver = (signal: SessionSignal) => void

// Public routes authenticate nothing, so a response from one is not evidence
// that the session is still alive and must not slide the local estimate.
const publicPathPrefixes = ["/healthz", "/api/v1/system", "/api/v1/auth/csrf"]

const observers = new Set<SessionObserver>()

let serverClockSkewMs = 0

export function slidesIdleWindow(pathname: string): boolean {
  return !publicPathPrefixes.some((prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`))
}

export function recordServerClock(dateHeader: string | null, receivedAt: number): void {
  const serverNow = dateHeader === null ? Number.NaN : Date.parse(dateHeader)
  if (!Number.isFinite(serverNow)) return
  serverClockSkewMs = serverNow - receivedAt
}

export function clockSkewMs(): number {
  return serverClockSkewMs
}

// A non-positive idle window means the server reported no usable idle deadline
// — an enrollment session carries the zero time — and the countdown then
// follows the absolute deadline alone instead of expiring the page at once.
export function sessionLifetime(
  instants: SessionInstants,
  skewMs: number,
  receivedAt: number,
): SessionLifetime | null {
  const absolute = Date.parse(instants.expires_at)
  if (!Number.isFinite(absolute)) return null
  const idle = Date.parse(instants.idle_expires_at)
  const idleWindowMs = Number.isFinite(idle) ? idle - (receivedAt + skewMs) : 0
  return {
    absoluteExpiresAt: absolute - skewMs,
    idleWindowMs: idleWindowMs > 0 ? idleWindowMs : Number.POSITIVE_INFINITY,
    lastActivityAt: receivedAt,
  }
}

export function withActivity(lifetime: SessionLifetime, at: number): SessionLifetime {
  return at > lifetime.lastActivityAt ? { ...lifetime, lastActivityAt: at } : lifetime
}

export function expiresAt(lifetime: SessionLifetime): number {
  return Math.min(lifetime.absoluteExpiresAt, lifetime.lastActivityAt + lifetime.idleWindowMs)
}

export function remainingMs(lifetime: SessionLifetime, now: number): number {
  return Math.max(0, expiresAt(lifetime) - now)
}

export function formatCountdown(remaining: number): string {
  const total = Math.max(0, Math.ceil(remaining / 1000))
  const seconds = String(total % 60).padStart(2, "0")
  const minutes = Math.floor(total / 60) % 60
  const hours = Math.floor(total / 3600)
  if (hours === 0) return `${minutes}:${seconds}`
  return `${hours}:${String(minutes).padStart(2, "0")}:${seconds}`
}

export function observeSession(observer: SessionObserver): () => void {
  observers.add(observer)
  return () => {
    observers.delete(observer)
  }
}

export function reportSession(signal: SessionSignal): void {
  for (const observer of [...observers]) observer(signal)
}
