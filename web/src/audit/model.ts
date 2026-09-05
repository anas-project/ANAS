// REQUIREMENTS: CONSOLE-R-128 CONSOLE-R-153 CONSOLE-R-180
import type { AuditEvent } from "../api/audit"

export interface VisibleAuditEvent {
  sequence: number
  timestamp: string
  type: string
  actor: string
  workspace: string
  outcome: string
  details: string
}

// The writer redacts before persisting, so the console never has to decide what
// is safe to show. It only has to render each record as untrusted text.
export function visibleAuditEvent(event: AuditEvent): VisibleAuditEvent {
  return {
    sequence: event.sequence,
    timestamp: event.timestamp,
    type: event.type,
    actor: event.actor ?? "—",
    workspace: event.workspace_id ?? "—",
    outcome: event.outcome ?? "—",
    details: formatAuditDetails(event.details),
  }
}

export function formatAuditDetails(details: AuditEvent["details"]): string {
  if (!details) return ""
  return Object.keys(details)
    .sort()
    .map((key) => `${key}=${formatDetailValue(details[key])}`)
    .join(" ")
}

function formatDetailValue(value: unknown): string {
  if (value === null || value === undefined) return "—"
  if (typeof value === "string") return value
  if (typeof value === "number" || typeof value === "boolean") return String(value)
  return JSON.stringify(value)
}

// A page without a cursor is the end of the journal. Treating an empty string
// the same way keeps a malformed response from looping forever.
export function nextAuditCursor(cursor: string | null | undefined): string | null {
  return cursor === null || cursor === undefined || cursor === "" ? null : cursor
}

export function appendAuditPage(
  existing: readonly VisibleAuditEvent[],
  page: readonly AuditEvent[],
): VisibleAuditEvent[] {
  const seen = new Set(existing.map((item) => item.sequence))
  const merged = [...existing]
  for (const event of page) {
    if (seen.has(event.sequence)) continue
    seen.add(event.sequence)
    merged.push(visibleAuditEvent(event))
  }
  return merged.sort((left, right) => right.sequence - left.sequence)
}
