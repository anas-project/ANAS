import { describe, expect, it } from "vitest"

import { leaseDiagram, tierSummary } from "./model"

const directions = [
  { flow: "egress" as const, peer: "internet" as const, allowed: true },
  { flow: "egress" as const, peer: "lan" as const, allowed: false },
  { flow: "egress" as const, peer: "modules" as const, allowed: true },
  { flow: "ingress" as const, peer: "traefik" as const, allowed: false },
]

describe("lease network diagram", () => {
  it("puts outbound peers left and inbound sources right, one row each", () => {
    const diagram = leaseDiagram({ directions })
    expect(diagram.outbound.map((r) => r.peer)).toEqual(["internet", "lan", "modules"])
    expect(diagram.inbound.map((r) => r.peer)).toEqual(["traefik"])
    expect(diagram.outbound.map((r) => r.allowed)).toEqual([true, false, true])
    expect(diagram.columns.outbound).toBeLessThan(diagram.columns.lease)
    expect(diagram.columns.lease).toBeLessThan(diagram.columns.inbound)
    for (const row of [...diagram.outbound, ...diagram.inbound]) {
      expect(row.y).toBeGreaterThan(diagram.lease.y)
      expect(row.y).toBeLessThan(diagram.lease.y + diagram.lease.height)
    }
  })

  it("keeps a lease with no directions drawable", () => {
    const diagram = leaseDiagram({ directions: [] })
    expect(diagram.height).toBeGreaterThan(0)
    expect(diagram.outbound).toEqual([])
  })

  it("summarizes tiers and only the switches that are on", () => {
    expect(tierSummary({ egress: "internet", ingress: "none", module_access: true, intra_lease: false })).toEqual(["internet", "none", "module_access"])
  })
})
