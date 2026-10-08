<script setup lang="ts">
// REQUIREMENTS: TEMP-R-024 TEMP-R-025
import { computed } from "vue"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"

const props = defineProps<{ modules: components["schemas"]["ModuleRuntimeStatus"][]; locale: Locale }>()
const text = computed(() => messages[props.locale])
const declared = computed(() => props.modules.filter((module) => module.temp_storage !== undefined))
function stateLabel(state: string): string {
  switch (state) {
    case "ok": return text.value.tempStorageOK
    case "low_space": return text.value.tempStorageLow
    case "unavailable": return text.value.tempStorageUnavailable
    case "not_applicable": return text.value.tempStorageNotApplicable
    default: return text.value.tempStorageUnknown
  }
}
function storageProblem(state: string | undefined): boolean {
  return state !== undefined && state !== "ok" && state !== "not_applicable"
}
</script>

<template>
  <section v-if="declared.length" class="plan-preview" data-temp-storage-status>
    <h3>{{ text.tempStorageTitle }}</h3>
    <article v-for="module in declared" :key="module.module" :role="storageProblem(module.temp_storage?.state) ? 'alert' : 'status'">
      <p><strong>{{ module.module }}</strong>: {{ stateLabel(module.temp_storage?.state ?? "unknown") }}</p>
      <ul v-if="module.temp_storage?.issues.length" class="compact-list">
        <li v-for="issue in module.temp_storage.issues" :key="`${issue.name}:${issue.code}`">
          {{ issue.name }}: <code>{{ issue.code }}</code>
        </li>
      </ul>
    </article>
  </section>
</template>
