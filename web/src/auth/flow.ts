// REQUIREMENTS: CONSOLE-R-103 CONSOLE-R-104 CONSOLE-R-130
import type { components } from "../api/schema"

export type ConsoleRuntimeState = components["schemas"]["ConsoleRuntimeState"]
export type EntryPhase = "m0" | "bootstrap" | "enrollment-recovery" | "owner" | "login"

export function initialPhase(state: ConsoleRuntimeState, hasEnrollmentCSRF: boolean): EntryPhase {
  switch (state) {
    case "m0":
      return "m0"
    case "bootstrap":
      return "bootstrap"
    case "enrollment":
      return hasEnrollmentCSRF ? "owner" : "enrollment-recovery"
    case "full":
      return "login"
  }
}
