import { afterEach, describe, expect, it, vi } from "vitest"

import { openJobEventStream, parseJobEvent } from "./job-events"

class FakeEventSource {
  static last: FakeEventSource | null = null
  readonly listeners = new Map<string, (event: Event) => void>()
  closed = false

  constructor(readonly url: string) {
    FakeEventSource.last = this
  }

  addEventListener(type: string, listener: (event: Event) => void): void {
    this.listeners.set(type, listener)
  }

  close(): void {
    this.closed = true
  }

  emit(type: string, data?: string): void {
    const event = data === undefined ? new Event(type) : new MessageEvent(type, { data })
    this.listeners.get(type)?.(event)
  }
}

afterEach(() => {
  vi.unstubAllGlobals()
  FakeEventSource.last = null
})

describe("job EventSource client", () => {
  it("validates the closed event envelope and expected job", () => {
    const raw = JSON.stringify({
      api_version: "anas.dev/api/v1",
      id: 7,
      job_id: "job-a",
      timestamp: "2026-09-01T00:00:00Z",
      kind: "progress",
      data: { phase: "activate" },
    })
    expect(parseJobEvent(raw, "job-a")).toMatchObject({ id: 7, kind: "progress" })
    expect(() => parseJobEvent(raw, "job-b")).toThrow("job_event_invalid")
  })

  it("uses the same-origin encoded URL and separates data problems from reconnects", () => {
    vi.stubGlobal("window", { location: { origin: "https://nas.example" } })
    const onEvent = vi.fn()
    const onProblem = vi.fn()
    const onDisconnect = vi.fn()
    openJobEventStream("job/a", { onEvent, onProblem, onDisconnect }, FakeEventSource)
    const source = FakeEventSource.last!
    expect(source.url).toBe("https://nas.example/api/v1/jobs/job%2Fa/events")
    source.emit("job", JSON.stringify({
      api_version: "anas.dev/api/v1",
      id: 1,
      job_id: "job/a",
      timestamp: "2026-09-01T00:00:00Z",
      kind: "started",
    }))
    expect(onEvent).toHaveBeenCalledOnce()
    source.emit("error")
    expect(onDisconnect).toHaveBeenCalledOnce()
    source.emit("gap", JSON.stringify({ code: "event_history_gap" }))
    expect(onProblem).toHaveBeenCalledWith("event_history_gap")
    expect(source.closed).toBe(true)
  })
})
