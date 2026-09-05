// REQUIREMENTS: CONSOLE-R-185 CONSOLE-R-186
import createClient from "openapi-fetch"

import { problemCode } from "./problems"
import { recordServerClock, reportSession, slidesIdleWindow } from "./session"
import type { paths } from "./schema"

export const api = createClient<paths>({
  baseUrl: typeof window === "undefined" ? "http://localhost" : window.location.origin,
  credentials: "same-origin",
  fetch: (request) => globalThis.fetch(request),
})

function pathnameOf(url: string): string {
  try {
    return new URL(url).pathname
  } catch {
    return url
  }
}

// Both an expired session and a rejected password answer `401`, and only the
// problem code separates them: reacting to the status alone would throw the
// operator back to the entry screen for every mistyped password. The body is
// read from a clone so the caller still parses its own problem.
async function sessionRejected(response: Response): Promise<boolean> {
  try {
    return problemCode(await response.clone().json()) === "unauthenticated"
  } catch {
    return false
  }
}

// Every route but the public few authenticates the session cookie and slides
// its idle window server-side, so one response hook keeps the local expiry
// estimate honest and notices the moment the session stops being accepted —
// wherever in the console that happens.
api.use({
  async onResponse({ request, response }) {
    const receivedAt = Date.now()
    recordServerClock(response.headers.get("date"), receivedAt)
    if (response.status === 401) {
      if (await sessionRejected(response)) reportSession({ kind: "unauthenticated" })
      return
    }
    if (slidesIdleWindow(pathnameOf(request.url))) reportSession({ kind: "activity", at: receivedAt })
  },
})
