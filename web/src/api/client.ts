import createClient from "openapi-fetch"

import type { paths } from "./schema"

export const api = createClient<paths>({
  baseUrl: typeof window === "undefined" ? "http://localhost" : window.location.origin,
  credentials: "same-origin",
  fetch: (request) => globalThis.fetch(request),
})
