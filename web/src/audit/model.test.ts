import { describe, expect, it } from "vitest"

import { appendAuditPage, formatAuditDetails, nextAuditCursor, visibleAuditEvent } from "./model"

const base = { sequence: 1, timestamp: "2026-09-04T10:00:00Z", type: "auth.login" }

describe("audit model", () => {
  it("renders absent optional fields as a placeholder instead of undefined", () => {
    const visible = visibleAuditEvent(base)
    expect(visible.actor).toBe("—")
    expect(visible.workspace).toBe("—")
    expect(visible.outcome).toBe("—")
    expect(visible.details).toBe("")
  })

  it("formats details deterministically so two renders of one record match", () => {
    const details = { outcome: "ok", nested: { a: 1 }, count: 3, flag: true, missing: null }
    expect(formatAuditDetails(details)).toBe('count=3 flag=true missing=— nested={"a":1} outcome=ok')
    expect(formatAuditDetails(undefined)).toBe("")
  })

  it("treats an absent or empty cursor as the end of the journal", () => {
    expect(nextAuditCursor(null)).toBeNull()
    expect(nextAuditCursor(undefined)).toBeNull()
    expect(nextAuditCursor("")).toBeNull()
    expect(nextAuditCursor("cursor-2")).toBe("cursor-2")
  })

  it("merges pages newest first and never duplicates a sequence", () => {
    const first = appendAuditPage([], [{ ...base, sequence: 3 }, { ...base, sequence: 2 }])
    const merged = appendAuditPage(first, [{ ...base, sequence: 2 }, { ...base, sequence: 1 }])
    expect(merged.map((item) => item.sequence)).toEqual([3, 2, 1])
  })
})
