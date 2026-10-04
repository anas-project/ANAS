// REQUIREMENTS: INCUS-R-128 INCUS-R-129
import type { LeaseNetwork } from "../api/network"

// A fixed, dependency-free layout: outbound peers on the left, the lease in
// the middle, inbound sources on the right. Every row comes from the
// directions the server derived from the frozen tiers; nothing is inferred
// here, so the picture cannot claim more than the declaration allows.

export interface DiagramRow {
  peer: string
  allowed: boolean
  y: number
}

export interface LeaseDiagram {
  width: number
  height: number
  rowHeight: number
  columns: { outbound: number; lease: number; inbound: number; boxWidth: number }
  lease: { y: number; height: number }
  outbound: DiagramRow[]
  inbound: DiagramRow[]
}

const rowHeight = 34
const padding = 12
const boxWidth = 210

export function leaseDiagram(lease: Pick<LeaseNetwork, "directions">): LeaseDiagram {
  const outbound = lease.directions.filter((d) => d.flow === "egress")
  const inbound = lease.directions.filter((d) => d.flow === "ingress")
  const rows = Math.max(outbound.length, inbound.length, 1)
  const height = rows * rowHeight + padding * 2
  const place = (items: typeof outbound): DiagramRow[] => {
    // Center a shorter column against the taller one.
    const offset = padding + ((rows - items.length) * rowHeight) / 2
    return items.map((d, i) => ({ peer: d.peer, allowed: d.allowed, y: offset + i * rowHeight + rowHeight / 2 }))
  }
  return {
    width: boxWidth * 3 + 140,
    height,
    rowHeight,
    columns: { outbound: 0, lease: boxWidth + 70, inbound: boxWidth * 2 + 140, boxWidth },
    lease: { y: padding, height: height - padding * 2 },
    outbound: place(outbound),
    inbound: place(inbound),
  }
}

// The summary line under a lease title: tiers and switches in a fixed order.
export function tierSummary(lease: Pick<LeaseNetwork, "egress" | "ingress" | "module_access" | "intra_lease">): string[] {
  return [lease.egress, lease.ingress, lease.module_access ? "module_access" : "", lease.intra_lease ? "intra_lease" : ""].filter((v) => v !== "")
}
