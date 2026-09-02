// REQUIREMENTS: CONSOLE-R-120 CONSOLE-R-121 CONSOLE-R-126
import type { components } from "../api/schema"

export type ConfigCandidate = components["schemas"]["ConfigCandidate"]
export type ConfigDocument = components["schemas"]["ConfigPublicDocument"]
export type ConfigField = components["schemas"]["ConfigField"]
export type SensitiveOperation = "unchanged" | "set" | "unset"

const blockedPathSegments = new Set(["__proto__", "prototype", "constructor"])

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function assertSafePath(path: readonly string[]): void {
  if (path.length === 0 || path.some((segment) => segment.length === 0 || blockedPathSegments.has(segment))) {
    throw new Error("invalid_config_document_path")
  }
}

export function cloneConfig(document: ConfigDocument): ConfigDocument {
  // Vue exposes the live draft through a reactive Proxy, which the browser's
  // structured-clone algorithm rejects. The API document is deliberately
  // JSON-only, so a JSON round trip both unwraps the Proxy and preserves the
  // exact transport surface used by validate/PUT.
  return JSON.parse(JSON.stringify(document)) as ConfigDocument
}

export function documentValue(
  document: ConfigDocument,
  path: readonly string[],
): { present: boolean; value: unknown } {
  assertSafePath(path)
  let current: unknown = document
  for (let index = 0; index < path.length; index += 1) {
    const segment = path[index] as string
    if (!isRecord(current) || !Object.prototype.hasOwnProperty.call(current, segment)) {
      return { present: false, value: undefined }
    }
    current = current[segment]
  }
  return { present: true, value: current }
}

export function setDocumentValue(document: ConfigDocument, path: readonly string[], value: unknown): void {
  assertSafePath(path)
  let current: Record<string, unknown> = document
  for (const segment of path.slice(0, -1)) {
    const child = current[segment]
    if (!isRecord(child)) current[segment] = {}
    current = current[segment] as Record<string, unknown>
  }
  current[path[path.length - 1] as string] = value
}

export function removeDocumentValue(document: ConfigDocument, path: readonly string[]): void {
  assertSafePath(path)
  const parents: Array<{ object: Record<string, unknown>; key: string }> = []
  let current: Record<string, unknown> = document
  for (const segment of path.slice(0, -1)) {
    const child = current[segment]
    if (!isRecord(child)) return
    parents.push({ object: current, key: segment })
    current = child
  }
  delete current[path[path.length - 1] as string]

  // Keep an explicitly selected module even when its last editable field is
  // removed. The module object itself is the selection marker in config.yml.
  const minimumDepth = path[0] === "modules" && path.length > 2 ? 2 : 0
  for (let index = parents.length - 1; index >= minimumDepth; index -= 1) {
    const parent = parents[index] as { object: Record<string, unknown>; key: string }
    const child = parent.object[parent.key]
    if (!isRecord(child) || Object.keys(child).length !== 0) break
    delete parent.object[parent.key]
  }
}

export function configModuleNames(
  document: ConfigDocument,
  fields: readonly ConfigField[],
  availableModules: readonly string[] = [],
): string[] {
  const names = new Set<string>(availableModules)
  const modules = document.modules
  if (isRecord(modules)) {
    for (const name of Object.keys(modules)) names.add(name)
  }
  for (const field of fields) {
    if (field.document_path[0] === "modules" && field.document_path.length > 1) {
      names.add(field.document_path[1] as string)
    }
  }
  return [...names].sort((left, right) => left.localeCompare(right))
}

export function moduleSelected(document: ConfigDocument, module: string): boolean {
  const modules = document.modules
  return isRecord(modules) && Object.prototype.hasOwnProperty.call(modules, module)
}

export function setModuleSelected(document: ConfigDocument, module: string, selected: boolean): void {
  assertSafePath(["modules", module])
  if (selected) {
    if (!isRecord(document.modules)) document.modules = {}
    const modules = document.modules as Record<string, unknown>
    if (!isRecord(modules[module])) modules[module] = {}
    return
  }
  if (!isRecord(document.modules)) return
  delete document.modules[module]
  if (Object.keys(document.modules).length === 0) delete document.modules
}

export function fieldModuleSelected(document: ConfigDocument, field: ConfigField): boolean {
  return field.document_path[0] !== "modules" || moduleSelected(document, field.document_path[1] as string)
}

export function parseFieldValue(field: ConfigField, raw: string): unknown {
  if (field.type === "bool") {
    if (raw === "true") return true
    if (raw === "false") return false
    throw new Error("config_boolean_invalid")
  }
  if (field.type === "int") {
    if (!/^-?(?:0|[1-9][0-9]*)$/.test(raw)) throw new Error("config_integer_invalid")
    const value = Number(raw)
    if (!Number.isSafeInteger(value)) throw new Error("config_integer_invalid")
    return value
  }
  return raw
}

export function buildConfigCandidate(
  document: ConfigDocument,
  operations: Readonly<Record<string, SensitiveOperation>>,
  sensitiveValues: Readonly<Record<string, string>>,
): ConfigCandidate {
  const sensitive: NonNullable<ConfigCandidate["sensitive"]> = {}
  for (const [path, operation] of Object.entries(operations)) {
    if (operation === "set") {
      if (!Object.prototype.hasOwnProperty.call(sensitiveValues, path) || sensitiveValues[path] === "") {
        throw new Error("config_sensitive_value_required")
      }
      sensitive[path] = { operation: "set", value: sensitiveValues[path] as string }
    } else if (operation === "unset") {
      sensitive[path] = { operation: "unset" }
    }
  }
  const candidate: ConfigCandidate = { config: cloneConfig(document) }
  if (Object.keys(sensitive).length > 0) candidate.sensitive = sensitive
  return candidate
}

export function validatorFromETag(etag: string | null): string | null {
  if (etag === null) return null
  const match = /^"(cfgv-[0-9a-f]{64})"$/.exec(etag)
  return match?.[1] ?? null
}
