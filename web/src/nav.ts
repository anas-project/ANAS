// REQUIREMENTS: CONSOLE-R-125 CONSOLE-R-129
// The daemon serves the SPA from a fixed set of registered routes
// (CONSOLE-R-053); no catch-all exists, so a history-mode deep link would 404
// on refresh. Hash sections give each page its own URL, working back/forward
// and a refresh-safe address without adding a route to the server or a router
// dependency to the bundle.
import { watch, type Ref } from "vue"

export const consoleSections = [
  "overview",
  "config",
  "deployment",
  "lifecycle",
  "modules",
  "network",
  "maintenance",
  "jobs",
  "audit",
  "access",
] as const

export type ConsoleSection = (typeof consoleSections)[number]

export function parseSection(hash: string): ConsoleSection | null {
  const value = hash.replace(/^#\/?/, "")
  return (consoleSections as readonly string[]).includes(value) ? (value as ConsoleSection) : null
}

// An unknown or absent hash lands on the overview rather than an empty page.
export function sectionFromLocation(hash: string, fallback: ConsoleSection = "overview"): ConsoleSection {
  return parseSection(hash) ?? fallback
}

export function sectionHref(section: ConsoleSection): string {
  return `#/${section}`
}

// Sections whose routes only exist for an authenticated owner are hidden rather
// than offered as links that would fail.
export function visibleSections(options: {
  canConfigure: boolean
  authenticated: boolean
  canRecoverJobs: boolean
}): ConsoleSection[] {
  const visible: ConsoleSection[] = ["overview"]
  if (options.canConfigure) visible.push("config", "deployment")
  if (options.authenticated) visible.push("lifecycle", "modules", "network", "maintenance")
  if (options.canRecoverJobs) visible.push("jobs")
  if (options.authenticated) visible.push("audit")
  visible.push("access")
  return visible
}

// The caller owns session recovery and provides the resolved access surface.
// This watcher is scoped to the component's existing Vue effect scope.
export function guardSectionAccess(
  section: Ref<ConsoleSection>,
  available: Readonly<Ref<readonly ConsoleSection[]>>,
  recoveringSession: Readonly<Ref<boolean>>,
) {
  return watch([section, available, recoveringSession], ([selected, choices, pending]) => {
    // Discovery can reveal workspaces before the HttpOnly session has been
    // restored. That intermediate anonymous surface is not a resolved denial.
    // Rendering and requests retain their own authenticated access guards.
    if (!pending && !choices.includes(selected)) section.value = "overview"
  }, { immediate: true })
}
