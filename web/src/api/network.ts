// REQUIREMENTS: INCUS-R-128
import { api } from "./client"
import { APIProblemError } from "./problems"
import type { components } from "./schema"

export type LeaseNetworkResponse = components["schemas"]["ComputeLeaseNetworkResponse"]
export type LeaseNetwork = components["schemas"]["ComputeLeaseNetwork"]

export async function getWorkspaceLeaseNetworks(workspace: string): Promise<LeaseNetworkResponse> {
  const { data, error } = await api.GET("/api/v1/workspaces/{ws}/compute/leases", {
    params: { path: { ws: workspace } },
  })
  if (error || data === undefined) throw new APIProblemError(error)
  return data
}
