import { describe, expect, it } from "vitest"

import { initialPhase } from "./flow"

describe("console entry flow", () => {
  it("selects the bootstrap and full-state local-login screens", () => {
    expect(initialPhase("m0", false)).toBe("m0")
    expect(initialPhase("bootstrap", false)).toBe("bootstrap")
    expect(initialPhase("full", false)).toBe("login")
  })

  it("uses only the readable enrollment CSRF marker to resume owner enrollment", () => {
    expect(initialPhase("enrollment", false)).toBe("enrollment-recovery")
    expect(initialPhase("enrollment", true)).toBe("owner")
  })
})
