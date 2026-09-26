import { describe, expect, it } from "vitest"
import { computed, effectScope, nextTick, ref } from "vue"

import { consoleSections, guardSectionAccess, parseSection, sectionFromLocation, sectionHref, visibleSections, type ConsoleSection } from "./nav"

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

describe("navigation while restoring an existing owner session", () => {
  function fixture() {
    const scope = effectScope()
    const section = ref<ConsoleSection>("maintenance")
    const recovering = ref(true)
    const owner = ref(false)
    const workspacesKnown = ref(false)
    const available = computed(() => visibleSections({ canConfigure: owner.value,
      authenticated: owner.value, canRecoverJobs: workspacesKnown.value }))
    scope.run(() => guardSectionAccess(section, available, recovering))
    return { scope, section, recovering, owner, workspacesKnown }
  }

  it("keeps a deep link across system discovery until the actual session is restored", async () => {
    const f = fixture()
    try {
      f.workspacesKnown.value = true
      await nextTick()
      expect(f.section.value).toBe("maintenance")
      f.owner.value = true
      f.recovering.value = false
      await nextTick()
      expect(f.section.value).toBe("maintenance")
    } finally { f.scope.stop() }
  })

  it("rejects a private deep link when session recovery finishes unauthenticated", async () => {
    const f = fixture()
    try {
      f.recovering.value = false
      await nextTick()
      expect(f.section.value).toBe("overview")
      f.section.value = "maintenance"
      await nextTick()
      expect(f.section.value).toBe("overview")
    } finally { f.scope.stop() }
  })

  it("leaves public navigation intact and still redirects after owner access is lost", async () => {
    const f = fixture()
    try {
      f.section.value = "access"
      f.recovering.value = false
      await nextTick()
      expect(f.section.value).toBe("access")
      f.owner.value = true
      await nextTick()
      f.section.value = "maintenance"
      await nextTick()
      expect(f.section.value).toBe("maintenance")
      f.owner.value = false
      await nextTick()
      expect(f.section.value).toBe("overview")
    } finally { f.scope.stop() }
  })
})
