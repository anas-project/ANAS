// REQUIREMENTS: CONSOLE-R-125 CONSOLE-R-156
import type { components } from "../api/schema"

import { terminalJobStatus } from "../deployment/model"

type JobSummary = components["schemas"]["JobSummary"]
type JobDetail = components["schemas"]["JobDetail"]
type JobEvent = components["schemas"]["JobEvent"]

export interface VisibleJobEvent {
  id: number
  kind: string
  text: string
}

export function restoredJobID(items: readonly JobSummary[]): string | null {
  return items.find((item) => !terminalJobStatus(item.status))?.id ?? items[0]?.id ?? null
}

export function replaceJobSummary(items: readonly JobSummary[], detail: JobDetail): JobSummary[] {
  const summary: JobSummary = {
    id: detail.id,
    kind: detail.kind,
    workspace_id: detail.workspace_id,
    mutating: detail.mutating,
    status: detail.status,
    progress: detail.progress,
    created_at: detail.created_at,
    revision: detail.revision,
    ...(detail.started_at === undefined ? {} : { started_at: detail.started_at }),
    ...(detail.finished_at === undefined ? {} : { finished_at: detail.finished_at }),
  }
  const index = items.findIndex((item) => item.id === detail.id)
  if (index < 0) return [summary, ...items]
  const next = [...items]
  next[index] = summary
  return next
}

export function visibleJobEvent(event: JobEvent): VisibleJobEvent {
  let text = event.kind
  const data = event.data
  if (data && typeof data === "object") {
    if (typeof data.message === "string") {
      text = data.message
    } else if (event.kind === "progress" && typeof data.phase === "string") {
      const current = typeof data.current === "number" ? data.current : null
      const total = typeof data.total === "number" ? data.total : null
      text = current !== null && total !== null ? `${data.phase} ${current}/${total}` : data.phase
    }
  }
  return { id: event.id, kind: event.kind, text }
}

export function appendVisibleJobEvent(
  items: readonly VisibleJobEvent[],
  event: JobEvent,
  limit = 100,
): VisibleJobEvent[] {
  if (items.some((item) => item.id === event.id)) return [...items]
  return [...items, visibleJobEvent(event)].sort((left, right) => left.id - right.id).slice(-limit)
}
