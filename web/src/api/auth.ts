// REQUIREMENTS: CONSOLE-R-093 CONSOLE-R-103 CONSOLE-R-104 CONSOLE-R-126
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type BootstrapSession = components["schemas"]["BootstrapSessionResponse"]
type EnrollmentHandoff = components["schemas"]["EnrollmentHandoffResponse"]
type EnrollmentOwner = components["schemas"]["EnrollmentOwnerResponse"]
type LocalSession = components["schemas"]["LocalSessionResponse"]
type SessionRefresh = components["schemas"]["SessionRefreshResponse"]

export const enrollmentCSRFCookieName = "__Host-anas_enrollment_csrf"

function requestOrigin(): string {
  return window.location.origin
}

function requireData<T>(data: T | undefined, error: unknown): T {
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}

export function readCookie(name: string, cookie = document.cookie): string | null {
  for (const item of cookie.split(";")) {
    const separator = item.indexOf("=")
    if (separator < 0 || item.slice(0, separator).trim() !== name) continue
    try {
      return decodeURIComponent(item.slice(separator + 1))
    } catch {
      return null
    }
  }
  return null
}

export async function issuePreAuthCSRF(): Promise<string> {
  const { data, error } = await api.GET("/api/v1/auth/csrf")
  return requireData(data, error).csrf_token
}

export async function refreshAuthSession(): Promise<SessionRefresh> {
  const { data, error } = await api.GET("/api/v1/auth/session")
  return requireData(data, error)
}

export async function exchangeBootstrapToken(token: string, csrf: string): Promise<BootstrapSession> {
  const { data, error } = await api.POST("/api/v1/auth/bootstrap/exchange", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: { token },
  })
  return requireData(data, error)
}

export async function issueEnrollmentHandoff(csrf: string): Promise<EnrollmentHandoff> {
  const { data, error } = await api.POST("/api/v1/auth/enrollment/handoffs", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
  })
  return requireData(data, error)
}

export async function createInitialOwner(password: string, csrf: string): Promise<EnrollmentOwner> {
  const { data, error } = await api.POST("/api/v1/auth/enrollment/owner", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: { password },
  })
  return requireData(data, error)
}

export async function loginLocalOwner(password: string, csrf: string): Promise<LocalSession> {
  const { data, error } = await api.POST("/api/v1/auth/login", {
    params: { header: { Origin: requestOrigin() } },
    headers: { "X-CSRF-Token": csrf },
    body: { password },
  })
  return requireData(data, error)
}

export function validateEnrollmentHandoff(response: EnrollmentHandoff): URL {
  let target: URL
  let action: URL
  try {
    target = new URL(response.target_origin)
    action = new URL(response.form_action)
  } catch {
    throw new APIProblemError({ code: "enrollment_target_invalid" })
  }
  if (
    target.protocol !== "https:" ||
    target.username !== "" ||
    target.password !== "" ||
    target.pathname !== "/" ||
    target.search !== "" ||
    target.hash !== "" ||
    action.username !== "" ||
    action.password !== "" ||
    action.origin !== target.origin ||
    action.pathname !== "/api/v1/auth/enrollment/exchange" ||
    action.search !== "" ||
    action.hash !== ""
  ) {
    throw new APIProblemError({ code: "enrollment_target_invalid" })
  }
  return action
}

export function submitEnrollmentHandoff(document: Document, response: EnrollmentHandoff): void {
  const action = validateEnrollmentHandoff(response)
  const form = document.createElement("form")
  form.method = "post"
  form.action = action.href
  form.enctype = "application/x-www-form-urlencoded"
  form.target = "_top"
  form.hidden = true

  const handoff = document.createElement("input")
  handoff.type = "hidden"
  handoff.name = "handoff"
  handoff.value = response.handoff
  form.append(handoff)
  document.body.append(form)
  form.submit()
}
