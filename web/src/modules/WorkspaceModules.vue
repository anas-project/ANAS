<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-032
import { computed, onMounted, ref, watch } from "vue"

import { newIdempotencyKey } from "../api/deployment"
import {
  configureWorkspaceModule,
  getModuleCatalog,
  getWorkspaceModules,
  updateWorkspaceModules,
} from "../api/modules"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"

type ModuleList = components["schemas"]["ModuleListResponse"]
type ModuleCatalog = components["schemas"]["ModuleCatalogResponse"]
type ModuleState = components["schemas"]["ModuleState"]

const props = defineProps<{
  workspaceIds: string[]
  csrf: string
  locale: Locale
  refreshRevision: number
}>()
const emit = defineEmits<{ jobCreated: [jobID: string] }>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const snapshot = ref<ModuleList | null>(null)
const catalog = ref<ModuleCatalog | null>(null)
const etag = ref("")
const busy = ref(false)
const loading = ref(false)
const queuedJobID = ref("")
const errorCode = ref<string | null>(null)

const text = computed(() => messages[props.locale])
const errorText = computed(() => errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value))
const catalogReleases = computed(() => new Map((catalog.value?.catalog.modules ?? []).map((item) => [item.module, item.release])))

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

function release(value: string | null | undefined): string {
  return value ?? "—"
}

function catalogRelease(module: string): string {
  return catalogReleases.value.get(module) ?? "—"
}

function configurationStateLabel(state: ModuleState["configuration_state"]): string {
  if (state === "selected") return text.value.moduleConfigStateSelected
  if (state === "dependency") return text.value.moduleConfigStateDependency
  return text.value.moduleConfigStateAvailable
}

async function load(): Promise<void> {
  if (selectedWorkspace.value === "" || loading.value) return
  loading.value = true
  errorCode.value = null
  queuedJobID.value = ""
  try {
    const [modules, catalogResult] = await Promise.all([
      getWorkspaceModules(selectedWorkspace.value),
      getModuleCatalog(selectedWorkspace.value),
    ])
    if (modules.data.workspace_id !== selectedWorkspace.value || catalogResult.workspace_id !== selectedWorkspace.value) {
      throw new APIProblemError({ code: "module_response_invalid" })
    }
    snapshot.value = modules.data
    catalog.value = catalogResult
    etag.value = modules.etag
  } catch (error) {
    snapshot.value = null
    catalog.value = null
    etag.value = ""
    showError(error)
  } finally {
    loading.value = false
  }
}

async function queueRefresh(mode: "sync" | "update"): Promise<void> {
  if (busy.value || selectedWorkspace.value === "") return
  busy.value = true
  errorCode.value = null
  try {
    const result = await updateWorkspaceModules(
      selectedWorkspace.value,
      { mode },
      props.csrf,
      newIdempotencyKey(),
    )
    queuedJobID.value = result.job.id
    emit("jobCreated", result.job.id)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function configure(module: ModuleState): Promise<void> {
  if (busy.value || etag.value === "") return
  busy.value = true
  errorCode.value = null
  const action = module.configuration_state === "selected" ? "disable" : "enable"
  try {
    const result = await configureWorkspaceModule(
      selectedWorkspace.value,
      module.name,
      action,
      etag.value,
      props.csrf,
      newIdempotencyKey(),
    )
    queuedJobID.value = result.job.id
    emit("jobCreated", result.job.id)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

watch(() => props.workspaceIds, (ids) => {
  if (!ids.includes(selectedWorkspace.value)) selectedWorkspace.value = ids[0] ?? ""
})
watch(selectedWorkspace, () => void load())
watch(() => props.refreshRevision, () => void load())
onMounted(() => void load())
</script>

<template>
  <section class="deployment-card" aria-live="polite" data-workspace-modules>
    <div class="config-heading">
      <div>
        <p class="eyebrow">M3</p>
        <h2>{{ text.moduleManagementTitle }}</h2>
        <p>{{ text.moduleManagementHelp }}</p>
      </div>
      <label class="workspace-picker">
        <span>{{ text.workspace }}</span>
        <select v-model="selectedWorkspace" :disabled="busy || loading">
          <option v-for="workspace in workspaceIds" :key="workspace" :value="workspace">{{ workspace }}</option>
        </select>
      </label>
    </div>

    <div class="config-actions">
      <button type="button" class="secondary-button" :disabled="busy || loading" @click="load">
        {{ loading ? text.moduleLoading : text.reloadConfig }}
      </button>
      <button type="button" class="secondary-button" :disabled="busy || loading" @click="queueRefresh('sync')">
        {{ text.moduleSync }}
      </button>
      <button type="button" class="primary-button" :disabled="busy || loading" @click="queueRefresh('update')">
        {{ text.moduleUpdate }}
      </button>
    </div>

    <p v-if="snapshot" class="muted">
      {{ text.moduleActiveDeployment }} <code>{{ snapshot.active_deployment ?? text.none }}</code>
      · {{ text.moduleCatalogSource }} <code>{{ catalog?.catalog.source ?? "—" }}</code>
    </p>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ text.errorTitle }}</strong> {{ errorText }}
    </p>

    <div v-if="snapshot" class="module-grid">
      <article v-for="module in snapshot.modules" :key="module.name" class="module-card">
        <div class="module-card-heading">
          <div>
            <h3>{{ module.name }}</h3>
            <span class="effect-badge">{{ configurationStateLabel(module.configuration_state) }}</span>
          </div>
          <button type="button" class="text-button" :disabled="busy || etag === ''" @click="configure(module)">
            {{ module.configuration_state === "selected" ? text.moduleDisable : text.moduleEnable }}
          </button>
        </div>

        <dl class="module-state-list">
          <div><dt>{{ text.moduleInstalled }}</dt><dd>{{ release(module.installed_release) }}</dd></div>
          <div><dt>{{ text.moduleDesired }}</dt><dd>{{ release(module.desired_release) }}</dd></div>
          <div><dt>{{ text.moduleDeployed }}</dt><dd>{{ release(module.deployed_release) }}</dd></div>
          <div><dt>{{ text.moduleCatalog }}</dt><dd>{{ catalogRelease(module.name) }}</dd></div>
          <div><dt>{{ text.moduleRuntime }}</dt><dd>{{ module.runtime }}</dd></div>
          <div><dt>{{ text.moduleHealth }}</dt><dd>{{ module.health }}</dd></div>
          <div><dt>{{ text.moduleContainers }}</dt><dd>{{ module.containers }}</dd></div>
        </dl>

        <p class="muted">
          {{ text.moduleDependencies }}
          <span v-if="module.dependencies.length">{{ module.dependencies.join(", ") }}</span>
          <span v-else>{{ text.none }}</span>
        </p>
        <nav v-if="module.entry_points.length" class="module-entry-points" :aria-label="text.moduleEntryPoints">
          <a v-for="entry in module.entry_points" :key="entry.id" :href="entry.uri" target="_blank" rel="noopener noreferrer">
            {{ entry.id }}
          </a>
        </nav>
      </article>
    </div>

    <p v-if="queuedJobID" role="status">
      {{ text.moduleJobQueued }} <code>{{ queuedJobID }}</code>
    </p>
  </section>
</template>
