// REQUIREMENTS: CONSOLE-R-122 CONSOLE-R-131
import type { components } from "../api/schema"

type JobDetail = components["schemas"]["JobDetail"]
type JobStatus = components["schemas"]["JobStatus"]

export const riskyConfirmationWord = "APPLY"

export function confirmsRiskyApply(value: string): boolean {
  return value.trim() === riskyConfirmationWord
}

export function terminalJobStatus(status: JobStatus): boolean {
  return status === "succeeded" || status === "failed" || status === "canceled" || status === "interrupted"
}

export function guardedChangeBlockers(job: JobDetail): string[] {
  if (job.error?.code !== "guarded_changes") return []
  const blocked = job.error.detail?.blocked
  if (!Array.isArray(blocked)) return []
  return blocked.filter((item): item is string => typeof item === "string" && item.length > 0)
}

export function sortedEntries(record: Record<string, string>): [string, string][] {
  return Object.entries(record).sort(([left], [right]) => left.localeCompare(right))
}

export function sortedNestedEntries(
  record: Record<string, Record<string, string>>,
): [string, [string, string][]][] {
  return Object.entries(record)
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([name, values]) => [name, sortedEntries(values)])
}
