import { describe, expect, it } from "vitest"

import { consoleSections, parseSection, sectionFromLocation, sectionHref, visibleSections } from "./nav"

describe("console navigation", () => {
  it("accepts only known sections", () => {
    expect(parseSection("#/audit")).toBe("audit")
    expect(parseSection("audit")).toBe("audit")
    expect(parseSection("#/does-not-exist")).toBeNull()
    expect(parseSection("")).toBeNull()
  })

  it("falls back to the overview instead of rendering nothing", () => {
    expect(sectionFromLocation("#/nope")).toBe("overview")
    expect(sectionFromLocation("")).toBe("overview")
    expect(sectionFromLocation("#/jobs")).toBe("jobs")
  })

  it("round-trips every section through its href", () => {
    for (const section of consoleSections) {
      expect(parseSection(sectionHref(section))).toBe(section)
    }
  })

  it("hides sections whose routes the current session cannot reach", () => {
    const anonymous = visibleSections({ canConfigure: false, authenticated: false, canRecoverJobs: false })
    expect(anonymous).toEqual(["overview", "access"])

    const bootstrap = visibleSections({ canConfigure: true, authenticated: false, canRecoverJobs: true })
    expect(bootstrap).toContain("config")
    expect(bootstrap).not.toContain("audit")
    expect(bootstrap).not.toContain("maintenance")

    const owner = visibleSections({ canConfigure: true, authenticated: true, canRecoverJobs: true })
    expect(owner).toEqual([...consoleSections])
  })
})
