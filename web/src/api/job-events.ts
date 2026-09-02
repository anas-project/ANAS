// REQUIREMENTS: CONSOLE-R-022 CONSOLE-R-112 CONSOLE-R-125 CONSOLE-R-156
import type { components } from "./schema"

export type JobEvent = components["schemas"]["JobEvent"]

interface EventSourceClient {
  addEventListener(type: string, listener: (event: Event) => void): void
  close(): void
}

type EventSourceConstructor = new (url: string) => EventSourceClient

export interface JobEventHandlers {
  onEvent(event: JobEvent): void
  onProblem(code: string): void
  onDisconnect(): void
}

export function parseJobEvent(raw: string, expectedJobID: string): JobEvent {
  const value: unknown = JSON.parse(raw)
  if (typeof value !== "object" || value === null) throw new Error("job_event_invalid")
  const event = value as Partial<JobEvent>
  if (
    event.api_version !== "anas.dev/api/v1" ||
    !Number.isSafeInteger(event.id) ||
    Number(event.id) < 1 ||
    event.job_id !== expectedJobID ||
    typeof event.timestamp !== "string" ||
    typeof event.kind !== "string" ||
    event.kind === ""
  ) {
    throw new Error("job_event_invalid")
  }
  return event as JobEvent
}

function problemCode(raw: string, fallback: string): string {
  try {
    const value: unknown = JSON.parse(raw)
    if (typeof value === "object" && value !== null && "code" in value && typeof value.code === "string") {
      return value.code
    }
  } catch {
    // The closed fallback remains safe when a proxy or damaged stream returns non-JSON text.
  }
  return fallback
}

function messageData(event: Event): string | null {
  return "data" in event && typeof event.data === "string" ? event.data : null
}

export function openJobEventStream(
  jobID: string,
  handlers: JobEventHandlers,
  Constructor: EventSourceConstructor = EventSource as unknown as EventSourceConstructor,
): EventSourceClient {
  const path = `/api/v1/jobs/${encodeURIComponent(jobID)}/events`
  const source = new Constructor(new URL(path, window.location.origin).href)
  source.addEventListener("job", (browserEvent) => {
    const raw = messageData(browserEvent)
    if (raw === null) {
      handlers.onProblem("job_event_invalid")
      source.close()
      return
    }
    try {
      handlers.onEvent(parseJobEvent(raw, jobID))
    } catch {
      handlers.onProblem("job_event_invalid")
      source.close()
    }
  })
  source.addEventListener("gap", (browserEvent) => {
    handlers.onProblem(problemCode(messageData(browserEvent) ?? "", "event_gap"))
    source.close()
  })
  source.addEventListener("error", (browserEvent) => {
    const raw = messageData(browserEvent)
    if (raw !== null) {
      handlers.onProblem(problemCode(raw, "jobs_unavailable"))
      source.close()
      return
    }
    handlers.onDisconnect()
  })
  return source
}
