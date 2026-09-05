<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-128 CONSOLE-R-152 CONSOLE-R-153 CONSOLE-R-156 CONSOLE-R-180
import { onMounted, ref } from "vue"

import { listAuditEvents } from "../api/audit"
import { APIProblemError, problemMessage } from "../api/problems"
import { messages, type Locale } from "../i18n/messages"
import { appendAuditPage, nextAuditCursor, type VisibleAuditEvent } from "./model"

const props = defineProps<{
  locale: Locale
  apiVersion: string
  cliVersion: string
}>()

const pageSize = 50
const events = ref<VisibleAuditEvent[]>([])
const cursor = ref<string | null>(null)
const activity = ref<"load" | "more" | null>(null)
const loaded = ref(false)
const errorCode = ref<string | null>(null)

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

async function load(mode: "load" | "more"): Promise<void> {
  if (activity.value !== null) return
  activity.value = mode
  errorCode.value = null
  try {
    const page = await listAuditEvents(pageSize, mode === "more" ? (cursor.value ?? undefined) : undefined)
    events.value = mode === "more" ? appendAuditPage(events.value, page.items) : appendAuditPage([], page.items)
    cursor.value = nextAuditCursor(page.next_cursor)
    loaded.value = true
  } catch (error) {
    showError(error)
  } finally {
    activity.value = null
  }
}

onMounted(() => {
  void load("load")
})
</script>

<template>
  <section class="config-card" aria-labelledby="audit-title" aria-live="polite">
    <div class="config-heading">
      <div>
        <p class="eyebrow">{{ messages[props.locale].auditEyebrow }}</p>
        <h2 id="audit-title">{{ messages[props.locale].auditTitle }}</h2>
        <p>{{ messages[props.locale].auditHelp }}</p>
      </div>
      <button type="button" class="secondary-button" :disabled="activity !== null" @click="load('load')">
        {{ messages[props.locale].auditReload }}
      </button>
    </div>

    <dl>
      <div>
        <dt>{{ messages[props.locale].auditAPIVersion }}</dt>
        <dd><code>{{ props.apiVersion }}</code></dd>
      </div>
      <div>
        <dt>{{ messages[props.locale].auditCLIVersion }}</dt>
        <dd><code>{{ props.cliVersion }}</code></dd>
      </div>
    </dl>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ messages[props.locale].errorTitle }}</strong> {{ problemMessage(props.locale, errorCode) }}
    </p>
    <p v-if="activity === 'load'" class="muted">{{ messages[props.locale].auditLoading }}</p>
    <p v-else-if="loaded && events.length === 0" class="muted">{{ messages[props.locale].auditEmpty }}</p>

    <div v-else-if="events.length" class="audit-scroll">
      <table class="audit-table">
        <thead>
          <tr>
            <th scope="col">{{ messages[props.locale].auditSequence }}</th>
            <th scope="col">{{ messages[props.locale].auditTimestamp }}</th>
            <th scope="col">{{ messages[props.locale].auditType }}</th>
            <th scope="col">{{ messages[props.locale].auditActor }}</th>
            <th scope="col">{{ messages[props.locale].auditWorkspace }}</th>
            <th scope="col">{{ messages[props.locale].auditOutcome }}</th>
            <th scope="col">{{ messages[props.locale].auditDetails }}</th>
          </tr>
        </thead>
        <tbody>
          <!-- Every cell is interpolated, never rendered as raw markup:
               journal text stays untrusted even after the writer redacts it. -->
          <tr v-for="event in events" :key="event.sequence">
            <td>{{ event.sequence }}</td>
            <td>{{ event.timestamp }}</td>
            <td><code>{{ event.type }}</code></td>
            <td>{{ event.actor }}</td>
            <td>{{ event.workspace }}</td>
            <td>{{ event.outcome }}</td>
            <td class="audit-details">{{ event.details }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <button
      v-if="cursor !== null"
      type="button"
      class="secondary-button"
      :disabled="activity !== null"
      @click="load('more')"
    >
      {{ activity === "more" ? messages[props.locale].auditLoading : messages[props.locale].auditLoadMore }}
    </button>
  </section>
</template>
