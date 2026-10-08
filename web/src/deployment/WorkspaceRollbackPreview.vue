<script setup lang="ts">
// REQUIREMENTS: TEMP-R-038 TEMP-R-041
import { computed, ref, watch } from "vue"
import { previewWorkspaceRollback } from "../api/lifecycle"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"
import TempSwitchNotice from "./TempSwitchNotice.vue"

const props = defineProps<{ workspace: string; csrf: string; locale: Locale }>()
const target = ref("")
const preview = ref<components["schemas"]["RollbackPreview"] | null>(null)
const busy = ref(false)
const errorCode = ref<string | null>(null)
const text = computed(() => messages[props.locale])
const errorText = computed(() => errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value))

function invalidate(): void {
  preview.value = null
  errorCode.value = null
}

async function loadPreview(): Promise<void> {
  if (busy.value || props.workspace === "" || target.value.trim() === "") return
  const workspace = props.workspace
  const deployment = target.value.trim()
  busy.value = true
  invalidate()
  try {
    const response = await previewWorkspaceRollback(workspace, deployment, props.csrf)
    if (workspace !== props.workspace || deployment !== target.value.trim()) return
    if (response.workspace_id !== workspace || response.preview.target_deployment !== deployment) {
      throw new APIProblemError({ code: "rollback_response_invalid" })
    }
    preview.value = response.preview
  } catch (error) {
    if (workspace === props.workspace && deployment === target.value.trim()) {
      errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
    }
  } finally {
    busy.value = false
  }
}

watch(() => props.workspace, () => { target.value = ""; invalidate() })
watch(target, invalidate)
</script>

<template>
  <section class="plan-preview" data-rollback-preview>
    <h3>{{ text.rollbackPreviewTitle }}</h3>
    <p>{{ text.rollbackPreviewHelp }}</p>
    <label>
      <span>{{ text.rollbackTarget }}</span>
      <input v-model="target" type="text" autocomplete="off" spellcheck="false" :disabled="busy" />
    </label>
    <button type="button" class="secondary-button" :disabled="busy || target.trim() === ''" @click="loadPreview">
      {{ text.rollbackPreviewAction }}
    </button>
    <p v-if="errorCode" class="error-message" role="alert">{{ errorText }}</p>
    <div v-if="preview">
      <p>{{ text.lifecycleDeployment }} <code>{{ preview.active_deployment }}</code></p>
      <p>{{ text.rollbackTarget }}: <code>{{ preview.target_deployment }}</code></p>
      <TempSwitchNotice :plan="preview.temp_switch" :locale="locale" />
      <ul v-if="preview.guarded_changes.length" class="compact-list">
        <li v-for="change in preview.guarded_changes" :key="change">{{ change }}</li>
      </ul>
    </div>
  </section>
</template>
