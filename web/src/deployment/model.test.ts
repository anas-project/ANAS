import { describe, expect, it } from "vitest"

import type { components } from "../api/schema"
import {
  confirmsRiskyApply,
  guardedChangeBlockers,
  riskyConfirmationWord,
  sortedNestedEntries,
  terminalJobStatus,
} from "./model"

type JobDetail = components["schemas"]["JobDetail"]

function job(error?: JobDetail["error"]): JobDetail {
  return {
    id: "job-a",
    kind: "deployment.apply",
    workspace_id: "main",
    mutating: true,
    status: "failed",
    progress: 100,
    created_at: "2026-09-01T00:00:00Z",
    revision: 3,
    warnings: [],
    needs_compensation_check: false,
    ...(error === undefined ? {} : { error }),
  }
}

describe("deployment UI model", () => {
  it("requires the exact short risky confirmation word", () => {
    expect(confirmsRiskyApply(riskyConfirmationWord)).toBe(true)
    expect(confirmsRiskyApply(` ${riskyConfirmationWord} `)).toBe(true)
    expect(confirmsRiskyApply("apply")).toBe(false)
    expect(confirmsRiskyApply("")).toBe(false)
  })

  it("projects every guarded-change blocker without rewriting it", () => {
    const blocked = [
      "global.base_domain (immutable; migrate-service-domain)",
      "samba_dc.application_dns_mode (data_migrate; migrate-application-dns-zone)",
    ]
    expect(
      guardedChangeBlockers(
        job({ code: "guarded_changes", message: "safe", detail: { blocked } }),
      ),
    ).toEqual(blocked)
    expect(guardedChangeBlockers(job({ code: "plan_changed", message: "safe" }))).toEqual([])
  })

  it("recognizes terminal states and gives nested plan maps deterministic order", () => {
    expect(terminalJobStatus("running")).toBe(false)
    expect(terminalJobStatus("interrupted")).toBe(true)
    expect(sortedNestedEntries({ zeta: { zone: "z", mode: "auto" }, alpha: { provider: "a" } })).toEqual([
      ["alpha", [["provider", "a"]]],
      ["zeta", [["mode", "auto"], ["zone", "z"]]],
    ])
  })
})
