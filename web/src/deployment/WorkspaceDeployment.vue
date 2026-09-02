<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-054 CONSOLE-R-115 CONSOLE-R-117 CONSOLE-R-118 CONSOLE-R-122 CONSOLE-R-131
import { computed, onBeforeUnmount, ref, watch } from "vue"

import {
  applyWorkspaceDeployment,
  getJob,
  issueDeploymentStepUp,
  newIdempotencyKey,
  planWorkspaceDeployment,
} from "../api/deployment"
import { openJobEventStream } from "../api/job-events"
import { APIProblemError, problemMessage } from "../api/problems"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"
import {
  confirmsRiskyApply,
  guardedChangeBlockers,
  riskyConfirmationWord,
  sortedEntries,
  sortedNestedEntries,
  terminalJobStatus,
} from "./model"

type DeploymentPlanResponse = components["schemas"]["DeploymentPlanResponse"]
type JobDetail = components["schemas"]["JobDetail"]

const props = defineProps<{
  workspaceIds: string[]
  csrf: string
  locale: Locale
  consoleState: "bootstrap" | "full"
	authenticationSource: "local" | "oidc_proxy"
  configRevision: number
}>()
const emit = defineEmits<{ jobCreated: [jobID: string] }>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const ownerPassword = ref("")
const planResponse = ref<DeploymentPlanResponse | null>(null)
const stepUpProof = ref("")
const idempotencyKey = ref("")
const riskyWord = ref("")
const blockedChanges = ref<string[]>([])
const job = ref<JobDetail | null>(null)
const activity = ref<"plan" | "apply" | "job" | null>(null)
const errorCode = ref<string | null>(null)
let pollGeneration = 0
let jobEventStream: { close(): void } | null = null
let finishJobWait: (() => void) | null = null

const text = computed(() => messages[props.locale])
const fullState = computed(() => props.consoleState === "full")
const localAuthentication = computed(() => props.authenticationSource === "local")
const plan = computed(() => planResponse.value?.plan ?? null)
const errorText = computed(() =>
  errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value),
)
const canPlan = computed(
  () =>
    selectedWorkspace.value !== "" &&
    activity.value === null &&
	(!fullState.value || !localAuthentication.value || ownerPassword.value !== ""),
)
const allowRisky = computed(() => blockedChanges.value.length > 0)
const canApply = computed(
  () =>
    planResponse.value !== null &&
    activity.value === null &&
    (!allowRisky.value || confirmsRiskyApply(riskyWord.value)),
)
const modulePlans = computed(() => sortedNestedEntries(plan.value?.module_plans ?? {}))
const capabilityBindings = computed(() => sortedNestedEntries(plan.value?.capability_bindings ?? {}))
const dnsPlatforms = computed(() => sortedEntries(plan.value?.dns_platforms ?? {}))

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

function discardPlan(): void {
  if (planResponse.value !== null) planResponse.value.confirmation.token = ""
  planResponse.value = null
  stepUpProof.value = ""
  idempotencyKey.value = ""
}

function closeJobEventStream(): void {
  jobEventStream?.close()
  jobEventStream = null
}

function cancelJobWait(): void {
  closeJobEventStream()
  finishJobWait?.()
  finishJobWait = null
}

function invalidateForStateChange(): void {
  pollGeneration += 1
  cancelJobWait()
  discardPlan()
  blockedChanges.value = []
  riskyWord.value = ""
  job.value = null
  errorCode.value = null
  activity.value = null
}

function validPlanBinding(response: DeploymentPlanResponse): boolean {
  return (
    response.workspace_id === selectedWorkspace.value &&
    response.confirmation.action === "deployment.apply" &&
    response.confirmation.plan_job_id === response.job.id &&
    response.confirmation.config_validator === response.plan.config_validator &&
    response.confirmation.plan_digest === response.plan.digest
  )
}

async function generatePlan(): Promise<void> {
  if (!canPlan.value) return
  const password = ownerPassword.value
  ownerPassword.value = ""
  discardPlan()
  job.value = null
  errorCode.value = null
  activity.value = "plan"
  let proof = ""
  try {
    if (fullState.value) {
	  const stepUp = await issueDeploymentStepUp(
		selectedWorkspace.value,
		localAuthentication.value ? password : undefined,
		props.csrf,
	  )
      proof = stepUp.proof
    }
    const response = await planWorkspaceDeployment(
      selectedWorkspace.value,
      props.csrf,
      proof === "" ? undefined : proof,
    )
    if (!validPlanBinding(response)) throw new APIProblemError({ code: "plan_response_invalid" })
    emit("jobCreated", response.job.id)
    stepUpProof.value = proof
    proof = ""
    planResponse.value = response
    idempotencyKey.value = newIdempotencyKey()
  } catch (error) {
    proof = ""
    discardPlan()
    showError(error)
  } finally {
    activity.value = null
  }
}

async function waitForJob(jobID: string, generation: number): Promise<void> {
  activity.value = "job"
  try {
    await new Promise<void>((resolve) => {
      let settled = false
      let refreshRunning = false
      let refreshQueued = false

      const finish = (): void => {
        if (settled) return
        settled = true
        closeJobEventStream()
        if (finishJobWait === finish) finishJobWait = null
        resolve()
      }

      const refresh = async (): Promise<void> => {
        if (refreshRunning) {
          refreshQueued = true
          return
        }
        refreshRunning = true
        try {
          do {
            refreshQueued = false
            const response = await getJob(jobID)
            if (generation !== pollGeneration) {
              finish()
              return
            }
            job.value = response.job
            if (terminalJobStatus(response.job.status)) {
              const blockers = guardedChangeBlockers(response.job)
              if (blockers.length > 0) {
                blockedChanges.value = blockers
                riskyWord.value = ""
                discardPlan()
              } else if (response.job.status === "failed" || response.job.status === "interrupted") {
                errorCode.value = response.job.error?.code ?? "apply_failed"
              }
              finish()
              return
            }
          } while (refreshQueued && generation === pollGeneration)
        } catch (error) {
          if (generation === pollGeneration) showError(error)
          finish()
        } finally {
          refreshRunning = false
        }
      }

      finishJobWait = finish
      void (async () => {
        await refresh()
        if (settled || generation !== pollGeneration) return
        jobEventStream = openJobEventStream(jobID, {
          onEvent() {
            if (generation === pollGeneration) void refresh()
          },
          onProblem(code: string) {
            if (generation === pollGeneration) errorCode.value = code
            finish()
          },
          onDisconnect() {
            if (generation === pollGeneration) void refresh()
          },
        })
      })()
    })
  } finally {
    if (generation === pollGeneration) activity.value = null
  }
}

async function applyPlan(): Promise<void> {
  const planned = planResponse.value
  if (!canApply.value || planned === null) return
  activity.value = "apply"
  errorCode.value = null
  job.value = null
  const request: components["schemas"]["DeploymentApplyRequest"] = {
    plan_job_id: planned.confirmation.plan_job_id,
    confirmation_token: planned.confirmation.token,
    expected_config_validator: planned.plan.config_validator,
    expected_plan_digest: planned.plan.digest,
    build: false,
    update_lock: false,
    allow_risky: allowRisky.value,
    snapshot: false,
    no_snapshot: false,
    ...(fullState.value ? { step_up_proof: stepUpProof.value } : {}),
  }
  try {
    const response = await applyWorkspaceDeployment(
      selectedWorkspace.value,
      request,
      props.csrf,
      idempotencyKey.value,
    )
    emit("jobCreated", response.job.id)
    planned.confirmation.token = ""
    request.confirmation_token = ""
    if (request.step_up_proof !== undefined) request.step_up_proof = ""
    stepUpProof.value = ""
    planResponse.value = null
    const generation = ++pollGeneration
    await waitForJob(response.job.id, generation)
  } catch (error) {
    showError(error)
    activity.value = null
  }
}

watch(
  () => props.workspaceIds,
  (ids) => {
    if (!ids.includes(selectedWorkspace.value)) selectedWorkspace.value = ids[0] ?? ""
  },
)
watch(selectedWorkspace, invalidateForStateChange)
watch(() => props.configRevision, invalidateForStateChange)
onBeforeUnmount(() => {
  pollGeneration += 1
  cancelJobWait()
  ownerPassword.value = ""
  riskyWord.value = ""
  discardPlan()
})
</script>

<template>
  <section class="deployment-card" aria-live="polite">
    <div class="config-heading">
      <div>
        <p class="eyebrow">M1C</p>
        <h2>{{ text.deploymentTitle }}</h2>
        <p>{{ text.deploymentHelp }}</p>
      </div>
      <label class="workspace-picker">
        <span>{{ text.workspace }}</span>
        <select v-model="selectedWorkspace" :disabled="activity !== null">
          <option v-for="workspace in workspaceIds" :key="workspace" :value="workspace">{{ workspace }}</option>
        </select>
      </label>
    </div>

    <aside class="draft-notice" role="note">
      <strong>{{ text.planOnlyTitle }}</strong>
      <span>{{ text.planOnlyHelp }}</span>
    </aside>

	<div v-if="fullState && localAuthentication" class="step-up-form">
      <label for="deployment-step-up-password">{{ text.stepUpPassword }}</label>
      <input
        id="deployment-step-up-password"
        v-model="ownerPassword"
        type="password"
        autocomplete="current-password"
        :disabled="activity !== null"
      />
      <p>{{ text.stepUpHelp }}</p>
    </div>
	<p v-else-if="fullState" class="muted">{{ text.proxyStepUpHelp }}</p>
    <p v-else class="muted">{{ text.bootstrapPlanAuthorization }}</p>

    <button type="button" class="secondary-button" :disabled="!canPlan" @click="generatePlan">
      {{ activity === "plan" ? text.planning : text.generatePlan }}
    </button>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ text.errorTitle }}</strong> {{ errorText }}
    </p>

    <section v-if="plan" class="plan-preview">
      <div class="plan-heading">
        <div>
          <h3>{{ text.planPreview }}</h3>
          <p>{{ text.planExpires }} <time>{{ planResponse?.confirmation.expires_at }}</time></p>
        </div>
        <code>{{ plan.digest.slice(0, 12) }}…</code>
      </div>

      <div class="plan-grid">
        <article>
          <h4>{{ text.plannedModules }}</h4>
          <ul class="compact-list"><li v-for="module in plan.modules" :key="module">{{ module }}</li></ul>
        </article>
        <article>
          <h4>{{ text.iamPlan }}</h4>
          <p>{{ text.provider }}: <strong>{{ plan.iam.provider ?? text.none }}</strong></p>
          <ul class="compact-list">
            <li v-for="consumer in plan.iam.consumers" :key="`${consumer.module}:${consumer.interface}`">
              {{ consumer.module }} → {{ consumer.interface }}
            </li>
          </ul>
        </article>
        <article>
          <h4>{{ text.dynamicDNSPlan }}</h4>
          <p>{{ text.provider }}: <strong>{{ plan.dynamic_dns.provider ?? text.none }}</strong></p>
          <p>{{ text.automatic }}: {{ plan.dynamic_dns.automatic }}</p>
          <ul class="compact-list"><li v-for="module in plan.dynamic_dns.self_managed" :key="module">{{ module }}</li></ul>
        </article>
        <article>
          <h4>{{ text.moduleLifecycle }}</h4>
          <ul class="compact-list">
            <li v-for="item in plan.module_lifecycles" :key="item.module">{{ item.module }}: {{ item.status }}</li>
          </ul>
        </article>
      </div>

      <details v-if="modulePlans.length" class="plan-details">
        <summary>{{ text.modulePlans }}</summary>
        <dl v-for="[module, values] in modulePlans" :key="module" class="plan-record">
          <dt>{{ module }}</dt>
          <dd v-for="[name, value] in values" :key="name"><code>{{ name }}</code>: {{ value }}</dd>
        </dl>
      </details>
      <details v-if="capabilityBindings.length" class="plan-details">
        <summary>{{ text.capabilityBindings }}</summary>
        <dl v-for="[module, values] in capabilityBindings" :key="module" class="plan-record">
          <dt>{{ module }}</dt>
          <dd v-for="[name, value] in values" :key="name"><code>{{ name }}</code>: {{ value }}</dd>
        </dl>
      </details>
      <details v-if="dnsPlatforms.length || plan.dns_credential_compatibility.length" class="plan-details">
        <summary>{{ text.dnsPlan }}</summary>
        <ul class="compact-list">
          <li v-for="[module, platform] in dnsPlatforms" :key="module">{{ module }} → {{ platform }}</li>
          <li
            v-for="item in plan.dns_credential_compatibility"
            :key="`${item.left}:${item.right}:${item.platform}`"
          >
            {{ item.left }} / {{ item.right }}: {{ item.platform }} ({{ item.compatibility }})
          </li>
        </ul>
      </details>

      <button type="button" class="primary-button" :disabled="!canApply" @click="applyPlan">
        {{ activity === "apply" ? text.queueingApply : text.applyPlan }}
      </button>
    </section>

    <aside v-if="blockedChanges.length" class="risk-confirmation" role="alert">
      <strong>{{ text.riskyChangesTitle }}</strong>
      <p>{{ text.riskyChangesHelp }}</p>
      <ul>
        <li v-for="item in blockedChanges" :key="item">{{ item }}</li>
      </ul>
      <label for="risky-confirmation-word">
        {{ text.riskyConfirmationPrompt }} <code>{{ riskyConfirmationWord }}</code>
      </label>
      <input
        id="risky-confirmation-word"
        v-model="riskyWord"
        type="text"
        autocomplete="off"
        spellcheck="false"
        :disabled="activity !== null"
      />
      <p>{{ text.riskyReplanHelp }}</p>
    </aside>

    <section v-if="job" class="job-status" role="status">
      <h3>{{ text.applyJob }}</h3>
      <dl>
        <div><dt>{{ text.jobID }}</dt><dd><code>{{ job.id }}</code></dd></div>
        <div><dt>{{ text.jobStatus }}</dt><dd>{{ job.status }}</dd></div>
        <div><dt>{{ text.jobProgress }}</dt><dd>{{ job.progress }}%</dd></div>
      </dl>
      <p v-if="activity === 'job'" class="muted">{{ text.followingJob }}</p>
    </section>
  </section>
</template>
