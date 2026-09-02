import { describe, expect, it } from "vitest"

import type { components } from "../api/schema"
import { appendVisibleJobEvent, replaceJobSummary, restoredJobID, visibleJobEvent } from "./model"

type JobSummary = components["schemas"]["JobSummary"]

function summary(id: string, status: JobSummary["status"], revision = 1): JobSummary {
  return {
    id,
    kind: "deployment.apply",
    workspace_id: "main",
    mutating: true,
    status,
    progress: status === "succeeded" ? 100 : 20,
    created_at: "2026-09-01T00:00:00Z",
    revision,
  }
}

describe("durable job drawer model", () => {
  it("restores the newest non-terminal job from the server page", () => {
    expect(restoredJobID([summary("done", "succeeded"), summary("active", "running")])).toBe("active")
    expect(restoredJobID([summary("done", "succeeded")])).toBe("done")
    expect(restoredJobID([])).toBeNull()
  })

  it("updates the server summary from a freshly polled detail", () => {
    const items = [summary("active", "running")]
    const next = replaceJobSummary(items, {
      ...summary("active", "succeeded", 2),
      progress: 100,
      warnings: [],
      needs_compensation_check: false,
    })
    expect(next[0]).toMatchObject({ id: "active", status: "succeeded", progress: 100, revision: 2 })
    expect(items[0]?.status).toBe("running")
  })

  it("keeps replayed events ordered, deduplicated, bounded, and text-only", () => {
    const hostile = {
      api_version: "anas.dev/api/v1" as const,
      id: 2,
      job_id: "active",
      timestamp: "2026-09-01T00:00:02Z",
      kind: "log",
      data: { level: "info", message: '<img src=x onerror="alert(1)">' },
    }
    expect(visibleJobEvent(hostile).text).toBe('<img src=x onerror="alert(1)">')
    const first = { ...hostile, id: 1, data: { message: "first" } }
    const events = appendVisibleJobEvent(appendVisibleJobEvent([visibleJobEvent(hostile)], first), hostile)
    expect(events.map((event) => event.id)).toEqual([1, 2])
    expect(appendVisibleJobEvent(events, { ...hostile, id: 3 }, 2).map((event) => event.id)).toEqual([2, 3])
  })
})
