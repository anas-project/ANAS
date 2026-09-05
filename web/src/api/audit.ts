// REQUIREMENTS: CONSOLE-R-152 CONSOLE-R-180
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

type AuditEventList = components["schemas"]["AuditEventListResponse"]

export type AuditEvent = components["schemas"]["AuditEvent"]

// The journal is append-only and is not modifiable through the API, so the
// console only ever reads it, newest first, one page at a time.
export async function listAuditEvents(limit: number, cursor?: string): Promise<AuditEventList> {
  const { data, error } = await api.GET("/api/v1/audit-events", {
    params: { query: cursor === undefined ? { limit } : { limit, cursor } },
  })
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}
