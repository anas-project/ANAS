<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-125 CONSOLE-R-126 CONSOLE-R-128 CONSOLE-R-185
import { onBeforeUnmount, onMounted, ref, watch } from "vue"

import { getJob, listJobs } from "../api/deployment"
import { openJobEventStream, type JobEvent } from "../api/job-events"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { reportSession } from "../api/session"
import { terminalJobStatus } from "../deployment/model"
import { messages, type Locale } from "../i18n/messages"
import {
  appendVisibleJobEvent,
  replaceJobSummary,
  restoredJobID,
  type VisibleJobEvent,
} from "./model"

type JobSummary = components["schemas"]["JobSummary"]
type JobDetail = components["schemas"]["JobDetail"]

const props = defineProps<{
  locale: Locale
  refreshRevision: number
}>()

const jobs = ref<JobSummary[]>([])
const selected = ref<JobDetail | null>(null)
const nextCursor = ref<string | null>(null)
const available = ref(false)
const activity = ref<"load" | "more" | null>(null)
const followingJobID = ref<string | null>(null)
const errorCode = ref<string | null>(null)
const events = ref<VisibleJobEvent[]>([])
let followGeneration = 0
let eventStream: { close(): void } | null = null
let detailRefreshRunning = false
let detailRefreshQueued = false

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

function closeEventStream(): void {
  eventStream?.close()
  eventStream = null
}

async function refreshJobDetail(jobID: string, generation: number): Promise<boolean> {
  const response = await getJob(jobID)
  if (generation !== followGeneration) return false
  selected.value = response.job
  jobs.value = replaceJobSummary(jobs.value, response.job)
  const terminal = terminalJobStatus(response.job.status)
  if (terminal) {
    followingJobID.value = null
    closeEventStream()
  }
  return terminal
}

async function queueJobDetailRefresh(jobID: string, generation: number): Promise<void> {
  if (detailRefreshRunning) {
    detailRefreshQueued = true
    return
  }
  detailRefreshRunning = true
  try {
    do {
      detailRefreshQueued = false
      await refreshJobDetail(jobID, generation)
    } while (detailRefreshQueued && generation === followGeneration)
  } catch (error) {
    if (generation === followGeneration) showError(error)
  } finally {
    detailRefreshRunning = false
  }
}

async function followJob(jobID: string, generation: number): Promise<void> {
  followingJobID.value = jobID
  events.value = []
  try {
    if (await refreshJobDetail(jobID, generation)) return
    if (generation !== followGeneration) return
    eventStream = openJobEventStream(jobID, {
      onEvent(event: JobEvent) {
        if (generation !== followGeneration) return
        events.value = appendVisibleJobEvent(events.value, event)
        void queueJobDetailRefresh(jobID, generation)
      },
      onProblem(code: string) {
        if (generation !== followGeneration) return
        // The event stream re-authorizes at every batch boundary but never
        // goes through the fetch client, so a session that ends while a job is
        // being followed is reported here instead.
        if (code === "unauthenticated") reportSession({ kind: "unauthenticated" })
        errorCode.value = code
        followingJobID.value = null
        closeEventStream()
      },
      onDisconnect() {
        if (generation === followGeneration) void queueJobDetailRefresh(jobID, generation)
      },
    })
  } catch (error) {
    if (generation === followGeneration) {
      followingJobID.value = null
      showError(error)
    }
  }
}

async function reloadJobs(): Promise<void> {
  const generation = ++followGeneration
  closeEventStream()
  activity.value = "load"
  errorCode.value = null
  selected.value = null
  followingJobID.value = null
  try {
    const response = await listJobs()
    if (generation !== followGeneration) return
    available.value = true
    jobs.value = response.items
    nextCursor.value = response.next_cursor
    activity.value = null
    const jobID = restoredJobID(response.items)
    if (jobID !== null) await followJob(jobID, generation)
  } catch (error) {
    if (generation === followGeneration) {
      if (error instanceof APIProblemError && error.code === "unauthenticated") {
        available.value = false
        jobs.value = []
        nextCursor.value = null
      } else {
        available.value = true
        showError(error)
      }
    }
  } finally {
    if (generation === followGeneration && activity.value === "load") activity.value = null
  }
}

async function loadMore(): Promise<void> {
  if (activity.value !== null || nextCursor.value === null) return
  activity.value = "more"
  errorCode.value = null
  try {
    const response = await listJobs(25, nextCursor.value)
    const seen = new Set(jobs.value.map((job) => job.id))
    jobs.value = [...jobs.value, ...response.items.filter((job) => !seen.has(job.id))]
    nextCursor.value = response.next_cursor
  } catch (error) {
    showError(error)
  } finally {
    activity.value = null
  }
}

async function selectJob(jobID: string): Promise<void> {
  if (followingJobID.value === jobID) return
  const generation = ++followGeneration
  closeEventStream()
  errorCode.value = null
  await followJob(jobID, generation)
}

watch(() => props.refreshRevision, reloadJobs)
onMounted(reloadJobs)
onBeforeUnmount(() => {
  followGeneration += 1
  closeEventStream()
})
</script>

<template>
  <section v-if="available" class="config-card job-drawer" aria-live="polite">
    <div class="config-heading">
      <div>
        <p class="eyebrow">M1C</p>
        <h2>{{ messages[locale].jobsTitle }}</h2>
        <p>{{ messages[locale].jobsHelp }}</p>
      </div>
      <button type="button" class="secondary-button" :disabled="activity !== null" @click="reloadJobs">
        {{ messages[locale].jobsReload }}
      </button>
    </div>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ messages[locale].errorTitle }}</strong> {{ problemMessage(locale, errorCode) }}
    </p>
    <p v-if="activity === 'load'" class="muted">{{ messages[locale].jobsLoading }}</p>
    <p v-else-if="jobs.length === 0" class="muted">{{ messages[locale].jobsEmpty }}</p>

    <ul v-else class="job-list">
      <li v-for="job in jobs" :key="job.id">
        <button
          type="button"
          :aria-current="selected?.id === job.id ? 'true' : undefined"
          @click="selectJob(job.id)"
        >
          <span><strong>{{ job.kind }}</strong> · {{ job.workspace_id }}</span>
          <span>{{ job.status }} · {{ job.progress }}%</span>
          <code>{{ job.id }}</code>
        </button>
      </li>
    </ul>

    <button
      v-if="nextCursor !== null"
      type="button"
      class="secondary-button"
      :disabled="activity !== null"
      @click="loadMore"
    >
      {{ activity === "more" ? messages[locale].jobsLoading : messages[locale].jobsLoadMore }}
    </button>

    <section v-if="selected" class="job-status" role="status">
      <h3>{{ messages[locale].jobDetails }}</h3>
      <dl>
        <div><dt>{{ messages[locale].jobID }}</dt><dd><code>{{ selected.id }}</code></dd></div>
        <div><dt>{{ messages[locale].jobStatus }}</dt><dd>{{ selected.status }}</dd></div>
        <div><dt>{{ messages[locale].jobProgress }}</dt><dd>{{ selected.progress }}%</dd></div>
        <div><dt>{{ messages[locale].jobWorkspace }}</dt><dd>{{ selected.workspace_id }}</dd></div>
      </dl>
      <p v-if="followingJobID === selected.id" class="muted">{{ messages[locale].followingJob }}</p>
      <p v-if="selected.error" class="error-message">
        <strong>{{ messages[locale].jobError }}</strong> {{ problemMessage(locale, selected.error.code) }}
      </p>
      <p v-if="selected.needs_compensation_check" class="error-message">
        {{ messages[locale].jobCompensation }}
      </p>
      <div v-if="selected.warnings.length" class="job-warnings">
        <strong>{{ messages[locale].jobWarnings }}</strong>
        <ul><li v-for="warning in selected.warnings" :key="warning">{{ warning }}</li></ul>
      </div>
      <div v-if="events.length" class="job-warnings">
        <strong>{{ messages[locale].jobEvents }}</strong>
        <ol>
          <li v-for="event in events" :key="event.id">
            <code>{{ event.kind }}</code> {{ event.text }}
          </li>
        </ol>
      </div>
    </section>
  </section>
</template>
