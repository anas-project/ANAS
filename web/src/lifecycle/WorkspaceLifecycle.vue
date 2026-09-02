<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-034 CONSOLE-R-054 CONSOLE-R-124
import { computed, onMounted, ref, watch } from "vue"

import {
  executeModuleLifecycle,
  getWorkspaceRuntime,
  previewModuleLifecycle,
} from "../api/lifecycle"
import { newIdempotencyKey } from "../api/deployment"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"

type LifecyclePreview = components["schemas"]["LifecyclePreviewResponse"]
type LifecycleAction = components["schemas"]["LifecyclePreview"]["action"]

const props = defineProps<{
  workspaceIds: string[]
  csrf: string
  locale: Locale
}>()
const emit = defineEmits<{ jobCreated: [jobID: string] }>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const action = ref<LifecycleAction>("restart")
const availableModules = ref<string[]>([])
const selectedModules = ref<string[]>([])
const preview = ref<LifecyclePreview | null>(null)
const queuedJobID = ref("")
const busy = ref(false)
const errorCode = ref<string | null>(null)

const text = computed(() => messages[props.locale])
const errorText = computed(() => errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value))
const canPreview = computed(() => selectedWorkspace.value !== "" && !busy.value)
const canExecute = computed(() => preview.value !== null && !busy.value)

function invalidatePreview(): void {
  preview.value = null
  queuedJobID.value = ""
  errorCode.value = null
}

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

async function loadModules(): Promise<void> {
  availableModules.value = []
  selectedModules.value = []
  invalidatePreview()
  if (selectedWorkspace.value === "") return
  try {
    const status = await getWorkspaceRuntime(selectedWorkspace.value)
    availableModules.value = status.module_runtime.map((item) => item.module)
  } catch (error) {
    showError(error)
  }
}

async function generateLifecyclePreview(): Promise<void> {
  if (!canPreview.value) return
  busy.value = true
  invalidatePreview()
  try {
    const response = await previewModuleLifecycle(
      selectedWorkspace.value,
      action.value,
      [...selectedModules.value],
      props.csrf,
    )
    if (response.workspace_id !== selectedWorkspace.value || response.preview.action !== action.value) {
      throw new APIProblemError({ code: "lifecycle_response_invalid" })
    }
    preview.value = response
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function confirmLifecycle(): Promise<void> {
  const current = preview.value
  if (!canExecute.value || current === null) return
  busy.value = true
  errorCode.value = null
  try {
    const response = await executeModuleLifecycle(
      selectedWorkspace.value,
      action.value,
      {
        modules: [...current.preview.requested_modules],
        expected_deployment_id: current.preview.deployment_id,
        expected_digest: current.preview.digest,
        confirmed_modules: [...current.preview.affected_modules],
      },
      props.csrf,
      newIdempotencyKey(),
    )
    queuedJobID.value = response.job.id
    emit("jobCreated", response.job.id)
    preview.value = null
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

watch(() => props.workspaceIds, (ids) => {
  if (!ids.includes(selectedWorkspace.value)) selectedWorkspace.value = ids[0] ?? ""
})
watch(selectedWorkspace, () => void loadModules())
watch(action, invalidatePreview)
watch(selectedModules, invalidatePreview, { deep: true })
onMounted(() => void loadModules())
</script>

<template>
  <section class="deployment-card" aria-live="polite" data-module-lifecycle>
    <div class="config-heading">
      <div>
        <p class="eyebrow">M2</p>
        <h2>{{ text.lifecycleTitle }}</h2>
        <p>{{ text.lifecycleHelp }}</p>
      </div>
      <label class="workspace-picker">
        <span>{{ text.workspace }}</span>
        <select v-model="selectedWorkspace" :disabled="busy">
          <option v-for="workspace in workspaceIds" :key="workspace" :value="workspace">{{ workspace }}</option>
        </select>
      </label>
    </div>

    <label>
      <span>{{ text.lifecycleAction }}</span>
      <select v-model="action" :disabled="busy">
        <option value="start">{{ text.lifecycleStart }}</option>
        <option value="stop">{{ text.lifecycleStop }}</option>
        <option value="restart">{{ text.lifecycleRestart }}</option>
      </select>
    </label>

    <fieldset :disabled="busy">
      <legend>{{ text.lifecycleTargets }}</legend>
      <p class="muted">{{ text.lifecycleTargetsHelp }}</p>
      <label v-for="module in availableModules" :key="module" class="module-option">
        <input v-model="selectedModules" type="checkbox" :value="module" />
        <span>{{ module }}</span>
      </label>
    </fieldset>

    <button type="button" class="secondary-button" :disabled="!canPreview" @click="generateLifecyclePreview">
      {{ busy ? text.working : text.lifecyclePreviewAction }}
    </button>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ text.errorTitle }}</strong> {{ errorText }}
    </p>

    <section v-if="preview" class="plan-preview" data-lifecycle-chain>
      <h3>{{ text.lifecycleActualChain }}</h3>
      <p>{{ text.lifecycleDeployment }} <code>{{ preview.preview.deployment_id }}</code></p>
      <ol class="compact-list">
        <li v-for="module in preview.preview.affected_modules" :key="module">{{ module }}</li>
      </ol>
      <p class="muted">{{ text.lifecycleConfirmHelp }}</p>
      <button type="button" class="primary-button" :disabled="!canExecute" @click="confirmLifecycle">
        {{ text.lifecycleConfirm }}
      </button>
    </section>

    <p v-if="queuedJobID" role="status">
      {{ text.lifecycleQueued }} <code>{{ queuedJobID }}</code>
    </p>
  </section>
</template>
