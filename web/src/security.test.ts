import { describe, expect, it } from "vitest"

import appSource from "./App.vue?raw"
import jobsSource from "./jobs/WorkspaceJobs.vue?raw"
import maintenanceSource from "./maintenance/WorkspaceMaintenance.vue?raw"

const sourceModules = import.meta.glob(["./**/*.ts", "./**/*.vue", "../emergency/**/*.ts"], {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>

describe("browser security and recovery boundaries", () => {
  it("has no persistent secret storage, telemetry sink, or raw HTML rendering", () => {
    const body = Object.entries(sourceModules)
      .filter(([path]) => !path.endsWith(".test.ts"))
      .map(([, source]) => source)
      .join("\n")
    for (const forbidden of [
      "localStorage",
      "sessionStorage",
      "indexedDB",
      "sendBeacon",
      "v-html",
      "innerHTML",
      "outerHTML",
      "insertAdjacentHTML",
    ]) {
      expect(body).not.toContain(forbidden)
    }
  })

  it("keeps workspace creation terminal-only and exposes recovery/access links", () => {
    expect(appSource).toContain("anas init")
    expect(appSource).toContain('download="anas-internal-ca.crt"')
    expect(appSource).toContain('href="/emergency"')
    expect(appSource).not.toMatch(/POST\([^\n]*workspaces/)
  })

  it("never publishes a local-account fallback from the trusted proxy origin", () => {
		expect(appSource).toContain('data.listener === "trusted_proxy"')
		expect(appSource).toContain('? "proxy-auth"')
		expect(appSource).toContain('v-if="system?.listener !== \'trusted_proxy\'" href="/emergency"')
		expect(appSource).toContain(":authentication-source=\"system.listener === 'trusted_proxy' ? 'oidc_proxy' : 'local'\"")
		expect(appSource).toContain("data-direct-recovery-origins")
		expect(appSource).toContain("{{ origin }}")
		expect(appSource).not.toMatch(/(?:href|:href)=[^\n]*direct_recovery_urls/)
	})

  it("renders job event and warning values only through Vue text interpolation", () => {
    expect(jobsSource).toContain("{{ event.text }}")
    expect(jobsSource).toContain("{{ warning }}")
    expect(jobsSource).not.toContain("v-html")
  })

  it("renders terminal commands only from the server descriptor", () => {
    expect(maintenanceSource).toContain("previewTerminalAction")
    expect(maintenanceSource).toContain("{{ descriptor.display }}")
    expect(maintenanceSource).toContain("descriptor.argv")
    expect(maintenanceSource).not.toMatch(/(?:display|argv)\s*[:=]\s*[^\n]*(?:join|`anas|\"anas|'anas)/)
  })
})
