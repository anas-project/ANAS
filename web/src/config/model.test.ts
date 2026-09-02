import { describe, expect, it } from "vitest"
import { reactive } from "vue"

import type { ConfigField } from "./model"
import {
  buildConfigCandidate,
  cloneConfig,
  configModuleNames,
  documentValue,
  moduleSelected,
  parseFieldValue,
  removeDocumentValue,
  setDocumentValue,
  setModuleSelected,
  validatorFromETag,
} from "./model"

function field(overrides: Partial<ConfigField> = {}): ConfigField {
  return {
    path: "demo.port",
    document_path: ["modules", "demo", "config", "port"],
    module: "demo",
    parameter: "port",
    type: "int",
    allowed_values: [],
    has_default: false,
    default_source: "none",
    input_required: false,
    must_resolve: false,
    constraints: {},
    sensitive: false,
    editable: true,
    effect: "reconcile",
    ...overrides,
  }
}

describe("configuration draft model", () => {
  it("clones a Vue reactive JSON draft into a plain transport document", () => {
    const draft = reactive({ modules: { demo: { config: { port: 80 } } } })
    const cloned = cloneConfig(draft)
    expect(cloned).toEqual(draft)
    expect(cloned).not.toBe(draft)
  })

  it("reads and writes the canonical server-provided document path", () => {
    const document = { modules: { demo: { config: { port: 80 } } } }
    expect(documentValue(document, field().document_path)).toEqual({ present: true, value: 80 })
    setDocumentValue(document, field().document_path, 443)
    expect(document.modules.demo.config.port).toBe(443)
  })

  it("removes an unset leaf while preserving the selected module marker", () => {
    const document = { modules: { demo: { config: { port: 80 } } } }
    removeDocumentValue(document, field().document_path)
    expect(document).toEqual({ modules: { demo: {} } })
    expect(moduleSelected(document, "demo")).toBe(true)
  })

  it("combines configured and schema-known modules and toggles selection", () => {
    const document = { modules: { existing: {} } }
    expect(configModuleNames(document, [field()], ["fieldless"])).toEqual(["demo", "existing", "fieldless"])
    setModuleSelected(document, "demo", true)
    expect(moduleSelected(document, "demo")).toBe(true)
    setModuleSelected(document, "existing", false)
    expect(document).toEqual({ modules: { demo: {} } })
  })

  it("builds explicit sensitive mutations without persisting unchanged values", () => {
    const candidate = buildConfigCandidate(
      { modules: { demo: {} } },
      { "demo.password": "set", "demo.old_token": "unset", "demo.keep": "unchanged" },
      { "demo.password": "private-value", "demo.keep": "must-not-appear" },
    )
    expect(candidate).toEqual({
      config: { modules: { demo: {} } },
      sensitive: {
        "demo.password": { operation: "set", value: "private-value" },
        "demo.old_token": { operation: "unset" },
      },
    })
    expect(() => buildConfigCandidate({}, { "demo.password": "set" }, {})).toThrow(
      "config_sensitive_value_required",
    )
  })

  it("parses typed values without accepting lossy integers", () => {
    expect(parseFieldValue(field(), "8443")).toBe(8443)
    expect(parseFieldValue(field({ type: "bool" }), "false")).toBe(false)
    expect(() => parseFieldValue(field(), "1.5")).toThrow("config_integer_invalid")
    expect(() => parseFieldValue(field(), "9007199254740992")).toThrow("config_integer_invalid")
  })

  it("accepts only canonical strong config ETags and blocks unsafe document paths", () => {
    const validator = `cfgv-${"a".repeat(64)}`
    expect(validatorFromETag(`"${validator}"`)).toBe(validator)
    expect(validatorFromETag(`W/"${validator}"`)).toBeNull()
    expect(() => setDocumentValue({}, ["__proto__", "polluted"], true)).toThrow(
      "invalid_config_document_path",
    )
    expect(({} as Record<string, unknown>).polluted).toBeUndefined()
  })
})
