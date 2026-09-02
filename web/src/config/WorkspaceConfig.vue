<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-120 CONSOLE-R-121 CONSOLE-R-126
import { computed, onBeforeUnmount, ref, watch } from "vue"

import { getWorkspaceConfig, putWorkspaceConfig, validateWorkspaceConfig } from "../api/config"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"
import {
  buildConfigCandidate,
  cloneConfig,
  configModuleNames,
  documentValue,
  fieldModuleSelected,
  moduleSelected,
  parseFieldValue,
  removeDocumentValue,
  setDocumentValue,
  setModuleSelected,
  validatorFromETag,
  type ConfigCandidate,
  type ConfigDocument,
  type ConfigField,
  type SensitiveOperation,
} from "./model"

type ConfigSnapshot = components["schemas"]["ConfigSnapshotResponse"]
type ConfigValidation = components["schemas"]["ConfigValidationResponse"]
type ConfigChange = components["schemas"]["ConfigChange"]

const props = defineProps<{
  workspaceIds: string[]
  csrf: string
  locale: Locale
}>()
const emit = defineEmits<{ saved: [workspace: string] }>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const snapshot = ref<ConfigSnapshot | null>(null)
const etag = ref<string | null>(null)
const draft = ref<ConfigDocument>({})
const validation = ref<ConfigValidation | null>(null)
const validatedCandidate = ref<ConfigCandidate | null>(null)
const savedChanges = ref<ConfigChange[]>([])
const sensitiveOperations = ref<Record<string, SensitiveOperation>>({})
const sensitiveValues = ref<Record<string, string>>({})
const fieldErrors = ref<Record<string, string>>({})
const activity = ref<"load" | "validate" | "save" | null>(null)
const errorCode = ref<string | null>(null)
const saved = ref(false)

const text = computed(() => messages[props.locale])
const fields = computed(() => snapshot.value?.fields ?? [])
const modules = computed(() =>
  configModuleNames(draft.value, fields.value, snapshot.value?.available_modules ?? []),
)
const groups = computed(() => {
  const grouped = new Map<string, ConfigField[]>()
  for (const field of fields.value) {
    const list = grouped.get(field.module) ?? []
    list.push(field)
    grouped.set(field.module, list)
  }
  return [...grouped.entries()]
    .map(([name, items]) => ({ name, fields: items.sort((left, right) => left.path.localeCompare(right.path)) }))
    .sort((left, right) => left.name.localeCompare(right.name))
})
const errorText = computed(() =>
  errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value),
)
const canSave = computed(() => validatedCandidate.value !== null && validation.value !== null && activity.value === null)

function clearPrivateDraft(): void {
  for (const path of Object.keys(sensitiveValues.value)) sensitiveValues.value[path] = ""
  sensitiveValues.value = {}
  sensitiveOperations.value = {}
  discardValidatedCandidate()
}

function discardValidatedCandidate(): void {
  for (const mutation of Object.values(validatedCandidate.value?.sensitive ?? {})) {
    if (mutation.operation === "set") mutation.value = ""
  }
  validatedCandidate.value = null
}

function invalidateValidation(): void {
  validation.value = null
  discardValidatedCandidate()
  savedChanges.value = []
  saved.value = false
  errorCode.value = null
}

function showError(error: unknown): void {
  if (error instanceof APIProblemError) {
    errorCode.value = error.code
  } else if (error instanceof Error && error.message.startsWith("config_")) {
    errorCode.value = error.message
  } else {
    errorCode.value = "request_failed"
  }
}

async function loadConfig(): Promise<void> {
  if (selectedWorkspace.value === "" || activity.value !== null) return
  activity.value = "load"
  errorCode.value = null
  saved.value = false
  clearPrivateDraft()
  validation.value = null
  savedChanges.value = []
  fieldErrors.value = {}
  snapshot.value = null
  try {
    const result = await getWorkspaceConfig(selectedWorkspace.value)
    snapshot.value = result.data
    etag.value = result.etag
    draft.value = cloneConfig(result.data.config)
  } catch (error) {
    showError(error)
  } finally {
    activity.value = null
  }
}

function currentFieldValue(field: ConfigField): unknown {
  return documentValue(draft.value, field.document_path).value
}

function fieldPresent(field: ConfigField): boolean {
  return documentValue(draft.value, field.document_path).present
}

function inputValue(field: ConfigField): string {
  const current = documentValue(draft.value, field.document_path)
  if (!current.present || current.value === null || current.value === undefined) return ""
  return String(current.value)
}

function fieldDisabled(field: ConfigField): boolean {
  return activity.value !== null || !field.editable || !fieldModuleSelected(draft.value, field)
}

function updateField(field: ConfigField, event: Event): void {
  const raw = (event.target as HTMLInputElement | HTMLSelectElement).value
  invalidateValidation()
  delete fieldErrors.value[field.path]
  if ((field.type === "bool" || field.type === "enum" || field.type === "int") && raw === "") {
    removeDocumentValue(draft.value, field.document_path)
    return
  }
  try {
    setDocumentValue(draft.value, field.document_path, parseFieldValue(field, raw))
  } catch (error) {
    fieldErrors.value[field.path] = error instanceof Error ? error.message : "config_candidate_invalid"
  }
}

function unsetField(field: ConfigField): void {
  invalidateValidation()
  delete fieldErrors.value[field.path]
  removeDocumentValue(draft.value, field.document_path)
}

function toggleModule(module: string, event: Event): void {
  invalidateValidation()
  const selected = (event.target as HTMLInputElement).checked
  setModuleSelected(draft.value, module, selected)
  if (!selected) {
    for (const field of fields.value) {
      if (field.document_path[0] === "modules" && field.document_path[1] === module) {
        sensitiveOperations.value[field.path] = "unchanged"
        sensitiveValues.value[field.path] = ""
      }
    }
  }
}

function sensitiveOperation(field: ConfigField): SensitiveOperation {
  return sensitiveOperations.value[field.path] ?? "unchanged"
}

function updateSensitiveOperation(field: ConfigField, event: Event): void {
  invalidateValidation()
  const operation = (event.target as HTMLSelectElement).value as SensitiveOperation
  sensitiveOperations.value[field.path] = operation
  if (operation !== "set") sensitiveValues.value[field.path] = ""
}

function updateSensitiveValue(field: ConfigField, event: Event): void {
  invalidateValidation()
  sensitiveValues.value[field.path] = (event.target as HTMLInputElement).value
}

function candidateForDraft(): ConfigCandidate {
  if (Object.keys(fieldErrors.value).length > 0) throw new Error("config_candidate_invalid")
  return buildConfigCandidate(draft.value, sensitiveOperations.value, sensitiveValues.value)
}

async function validateDraft(): Promise<void> {
  if (activity.value !== null || snapshot.value === null) return
  activity.value = "validate"
  errorCode.value = null
  saved.value = false
  validation.value = null
  discardValidatedCandidate()
  try {
    const candidate = candidateForDraft()
    const result = await validateWorkspaceConfig(selectedWorkspace.value, candidate, props.csrf)
    const currentValidator = validatorFromETag(etag.value)
    if (snapshot.value.managed && (currentValidator === null || result.base_validator !== currentValidator)) {
      throw new APIProblemError({ code: "config_precondition_failed" })
    }
    if (!snapshot.value.managed && result.base_validator !== undefined) {
      throw new APIProblemError({ code: "config_response_invalid" })
    }
    draft.value = cloneConfig(result.config)
    validation.value = result
    validatedCandidate.value = {
      config: cloneConfig(result.config),
      ...(candidate.sensitive === undefined ? {} : { sensitive: structuredClone(candidate.sensitive) }),
    }
  } catch (error) {
    showError(error)
  } finally {
    activity.value = null
  }
}

async function saveDraft(): Promise<void> {
  if (!canSave.value || snapshot.value === null || validatedCandidate.value === null) return
  activity.value = "save"
  errorCode.value = null
  try {
    const result = await putWorkspaceConfig(
      selectedWorkspace.value,
      validatedCandidate.value,
      props.csrf,
      snapshot.value.managed,
      etag.value,
    )
    savedChanges.value = result.data.changes
    snapshot.value = {
      api_version: result.data.api_version,
      workspace_id: result.data.workspace_id,
      managed: true,
      config: result.data.config,
      available_modules: result.data.available_modules,
      fields: result.data.fields,
    }
    etag.value = result.etag
    draft.value = cloneConfig(result.data.config)
    validation.value = null
    clearPrivateDraft()
    fieldErrors.value = {}
    saved.value = true
    emit("saved", selectedWorkspace.value)
  } catch (error) {
    showError(error)
  } finally {
    activity.value = null
  }
}

function fieldError(field: ConfigField): string {
  const code = fieldErrors.value[field.path]
  return code === undefined ? "" : problemMessage(props.locale, code)
}

watch(
  () => props.workspaceIds,
  (ids) => {
    if (!ids.includes(selectedWorkspace.value)) selectedWorkspace.value = ids[0] ?? ""
  },
)
watch(selectedWorkspace, loadConfig, { immediate: true })
onBeforeUnmount(clearPrivateDraft)
</script>

<template>
  <section class="config-card" aria-live="polite">
    <div class="config-heading">
      <div>
        <p class="eyebrow">M1C</p>
        <h2>{{ text.configTitle }}</h2>
        <p>{{ text.configHelp }}</p>
      </div>
      <label class="workspace-picker">
        <span>{{ text.workspace }}</span>
        <select v-model="selectedWorkspace" :disabled="activity !== null">
          <option v-for="workspace in workspaceIds" :key="workspace" :value="workspace">{{ workspace }}</option>
        </select>
      </label>
    </div>

    <p v-if="activity === 'load'" class="muted">{{ text.configLoading }}</p>
    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ text.errorTitle }}</strong> {{ errorText }}
    </p>

    <template v-if="snapshot">
      <aside class="draft-notice" role="note">
        <strong>{{ text.draftOnlyTitle }}</strong>
        <span>{{ text.draftOnly }}</span>
      </aside>

      <fieldset v-if="modules.length" class="module-selector" :disabled="activity !== null">
        <legend>{{ text.modules }}</legend>
        <label v-for="module in modules" :key="module">
          <input
            type="checkbox"
            :checked="moduleSelected(draft, module)"
            @change="toggleModule(module, $event)"
          />
          <span>{{ module }}</span>
        </label>
      </fieldset>

      <div class="field-groups">
        <details v-for="group in groups" :key="group.name" class="field-group" :open="group.name === 'global'">
          <summary>{{ group.name }} <span>{{ group.fields.length }}</span></summary>
          <div class="field-list">
            <article v-for="field in group.fields" :key="field.path" class="config-field">
              <div class="field-heading">
                <div>
                  <label :for="`config-${field.path}`">{{ field.parameter }}</label>
                  <code>{{ field.path }}</code>
                </div>
                <span class="effect-badge">{{ field.effect }}</span>
              </div>
              <p v-if="field.description" class="field-description">{{ field.description }}</p>

              <template v-if="field.sensitive">
                <p class="sensitive-state">
                  {{ text.currentSecret }}: <strong>{{ field.sensitive_state === "set" ? text.secretSet : text.secretUnset }}</strong>
                </p>
                <template v-if="field.editable">
                  <select
                    :id="`config-${field.path}`"
                    :value="sensitiveOperation(field)"
                    :disabled="fieldDisabled(field)"
                    @change="updateSensitiveOperation(field, $event)"
                  >
                    <option value="unchanged">{{ text.secretUnchanged }}</option>
                    <option value="set">{{ text.secretReplace }}</option>
                    <option value="unset">{{ text.secretRemove }}</option>
                  </select>
                  <input
                    v-if="sensitiveOperation(field) === 'set'"
                    type="password"
                    autocomplete="new-password"
                    :aria-label="`${field.path}: ${text.secretValue}`"
                    :value="sensitiveValues[field.path] ?? ''"
                    :disabled="fieldDisabled(field)"
                    required
                    @input="updateSensitiveValue(field, $event)"
                  />
                </template>
              </template>

              <template v-else-if="field.type === 'bool'">
                <select
                  :id="`config-${field.path}`"
                  :value="inputValue(field)"
                  :disabled="fieldDisabled(field)"
                  @change="updateField(field, $event)"
                >
                  <option value="">{{ text.useDefault }}</option>
                  <option value="true">true</option>
                  <option value="false">false</option>
                </select>
              </template>

              <template v-else-if="field.type === 'enum'">
                <select
                  :id="`config-${field.path}`"
                  :value="inputValue(field)"
                  :disabled="fieldDisabled(field)"
                  @change="updateField(field, $event)"
                >
                  <option value="">{{ text.useDefault }}</option>
                  <option v-for="value in field.allowed_values" :key="value" :value="value">{{ value }}</option>
                </select>
              </template>

              <input
                v-else
                :id="`config-${field.path}`"
                :type="field.type === 'int' ? 'number' : 'text'"
                :value="inputValue(field)"
                :placeholder="field.has_default ? field.default : undefined"
                :min="field.constraints.minimum"
                :max="field.constraints.maximum"
                :minlength="field.constraints.min_length"
                :maxlength="field.constraints.max_length"
                :pattern="field.constraints.pattern"
                :step="field.type === 'int' ? 1 : undefined"
                :disabled="fieldDisabled(field)"
                @input="updateField(field, $event)"
              />

              <div class="field-meta">
                <span v-if="field.input_required">{{ text.required }}</span>
                <span v-if="field.must_resolve">{{ text.mustResolve }}</span>
                <span v-if="field.apply">{{ text.applyWith }} <code>{{ field.apply }}</code></span>
              </div>
              <button
                v-if="!field.sensitive && field.editable && fieldPresent(field)"
                type="button"
                class="text-button"
                :disabled="fieldDisabled(field)"
                @click="unsetField(field)"
              >
                {{ text.unsetValue }}
              </button>
              <p v-if="!field.editable" class="guarded-field">
                {{ text.guardedField }} <code v-if="field.edit_command">{{ field.edit_command }}</code>
              </p>
              <p v-if="fieldError(field)" class="inline-error" role="alert">{{ fieldError(field) }}</p>
              <p v-if="!fieldModuleSelected(draft, field)" class="muted">{{ text.selectModuleFirst }}</p>
            </article>
          </div>
        </details>
      </div>

      <div class="config-actions">
        <button type="button" class="secondary-button" :disabled="activity !== null" @click="loadConfig">
          {{ text.reloadConfig }}
        </button>
        <button type="button" class="primary-button" :disabled="activity !== null" @click="validateDraft">
          {{ activity === "validate" ? text.validating : text.validateChanges }}
        </button>
      </div>

      <section v-if="validation" class="change-preview">
        <h3>{{ text.changePreview }}</h3>
        <p v-if="validation.changes.length === 0">{{ text.noChanges }}</p>
        <div v-else class="change-table-wrap">
          <table>
            <thead>
              <tr>
                <th>{{ text.path }}</th>
                <th>{{ text.change }}</th>
                <th>{{ text.effect }}</th>
                <th>{{ text.apply }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="change in validation.changes" :key="`${change.path}:${change.change}`">
                <td><code>{{ change.path }}</code><span v-if="change.sensitive"> {{ text.sensitive }}</span></td>
                <td>{{ change.change }}</td>
                <td>{{ change.effect }}</td>
                <td>{{ change.apply || "—" }}</td>
              </tr>
            </tbody>
          </table>
        </div>
        <button type="button" class="primary-button" :disabled="!canSave" @click="saveDraft">
          {{ activity === "save" ? text.saving : text.saveDesiredConfig }}
        </button>
      </section>

      <aside v-if="saved" class="save-notice" role="status">
        <strong>{{ text.savedTitle }}</strong>
        <span>{{ text.savedHelp }}</span>
        <span v-if="savedChanges.length">{{ savedChanges.length }} {{ text.savedChangeCount }}</span>
      </aside>
    </template>
  </section>
</template>
