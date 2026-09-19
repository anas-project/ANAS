<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from "vue"
import { newIdempotencyKey } from "../api/deployment"
import { getHostActionJob, invokeIncusHostApply, invokeIncusHostPlan, invokeIncusImagePruneApply, invokeIncusImagePrunePlan, issueHostActionConfirmation, type IncusHostPhase } from "../api/maintenance"
import { problemMessage } from "../api/problems"
import { clockSkewMs } from "../api/session"
import { messages, type Locale } from "../i18n/messages"
import { IncusApprovalFlow, type IncusFlowState, type IncusInput } from "./incus-flow"
import { IncusPruneFlow, type PruneState } from "./incus-prune-flow"

const props = withDefaults(defineProps<{ workspace: string; csrf: string; locale: Locale; disabled: boolean; imagePruneAvailable?: boolean; provisionAvailable?: boolean }>(), { imagePruneAvailable: false, provisionAvailable: true })
const emit = defineEmits<{ jobCreated: [id: string] }>()
const phase = ref<IncusHostPhase>("install")
const isolation = ref<"incus_container" | "incus_vm">("incus_container")
const storageGiB = ref(64)
const removePackages = ref(false)
const approved = ref(false)
const state = shallowRef<IncusFlowState>({ status: "idle", plan: null, jobID: "", errorCode: null })
const pruneApproved = ref(false)
const pruneState = shallowRef<PruneState>({ status: "idle", plan: null, errorCode: null, jobID: "" })
const pruneBusy = computed(() => pruneState.value.status === "planning" || pruneState.value.status === "applying")
const prunePlan = computed(() => pruneState.value.plan)
const prunePlanJob = computed(() => pruneState.value.plan?.id ?? "")
const pruneError = computed(() => pruneState.value.errorCode)
const text = computed(() => messages[props.locale])
const busy = computed(() => state.value.status === "planning" || state.value.status === "applying")
const input = (): IncusInput => ({ workspace: props.workspace, csrf: props.csrf, phase: phase.value,
  request: { interface: isolation.value, storage_size_gib: storageGiB.value, remove_packages: phase.value === "uninstall" && removePackages.value } })
const flow = new IncusApprovalFlow({
  plan: (value) => invokeIncusHostPlan(value.workspace, value.phase, value.request, value.csrf, newIdempotencyKey()),
  get: getHostActionJob,
  confirm: (value, id) => issueHostActionConfirmation(value.workspace, id, `incus.${value.phase}`, value.csrf),
  apply: (value, id, token, parameters) => invokeIncusHostApply(value.workspace, value.phase, id, token, parameters, value.csrf, newIdempotencyKey()),
}, (value) => { if (value.plan?.id !== state.value.plan?.id || value.status !== "ready") approved.value = false; state.value = value }, () => Date.now() + clockSkewMs())
async function apply(): Promise<void> {
  const id = await flow.execute(input(), approved.value)
  if (id !== null) emit("jobCreated", id)
}
const pruneInput = () => ({ workspace: props.workspace, csrf: props.csrf })
const pruneFlow = new IncusPruneFlow({
  plan: value => invokeIncusImagePrunePlan(value.workspace, value.csrf, newIdempotencyKey()),
  get: getHostActionJob,
  confirm: (value, id) => issueHostActionConfirmation(value.workspace, id, "incus.image-prune", value.csrf),
  apply: (value, id, token) => invokeIncusImagePruneApply(value.workspace, id, token, value.csrf, newIdempotencyKey()),
}, value => {
  if (value.plan?.id !== pruneState.value.plan?.id || value.status !== "ready") pruneApproved.value = false
  pruneState.value = value
}, () => Date.now() + clockSkewMs())
async function preparePrune(): Promise<void> { await pruneFlow.prepare(pruneInput()) }
async function applyPrune(): Promise<void> {
  const id = await pruneFlow.execute(pruneInput(), pruneApproved.value)
  if (id !== null) emit("jobCreated", id)
}
watch([() => props.workspace, () => props.csrf, phase, isolation, storageGiB, removePackages], () => { flow.reset(); pruneFlow.reset(); pruneApproved.value = false })
onBeforeUnmount(() => { flow.dispose(); pruneFlow.dispose() })
</script>

<template>
  <div class="incus-actions">
    <template v-if="provisionAvailable">
    <div class="maintenance-form-row">
      <label>{{ text.incusPhase }}<select v-model="phase" :disabled="disabled || busy">
        <option value="install">{{ text.incusPhaseInstall }}</option><option value="configure">{{ text.incusPhaseConfigure }}</option>
        <option value="enroll">{{ text.incusPhaseEnroll }}</option><option value="uninstall">{{ text.incusPhaseUninstall }}</option>
      </select></label>
      <label>{{ text.incusIsolation }}<select v-model="isolation" :disabled="disabled || busy">
        <option value="incus_container">{{ text.incusContainer }}</option><option value="incus_vm">{{ text.incusVM }}</option>
      </select></label>
      <label>{{ text.incusStorageGiB }}<input v-model.number="storageGiB" type="number" min="16" max="4096" step="1" :disabled="disabled || busy" /></label>
      <label v-if="phase === 'uninstall'" class="checkbox-line"><input v-model="removePackages" type="checkbox" :disabled="disabled || busy" />{{ text.incusRemovePackages }}</label>
      <button type="button" class="secondary-button" :disabled="disabled || busy || !workspace || !Number.isInteger(storageGiB) || storageGiB < 16 || storageGiB > 4096" @click="flow.prepare(input())">{{ text.incusPlanRun }}</button>
    </div>
    <p v-if="state.status === 'planning'" role="status">{{ text.incusPlanning }}</p>
    <p v-if="state.errorCode" class="error-message" role="alert">{{ problemMessage(locale, state.errorCode) }}</p>
    <div v-if="state.plan" class="plan-preview">
      <h4>{{ text.incusReviewPlan }}</h4>
      <p><code>{{ state.plan.id }}</code></p>
      <ul><li v-for="step in state.plan.steps" :key="step.id">{{ step.effect }}</li></ul>
      <p v-if="state.plan.steps.length === 0">{{ text.incusNoChanges }}</p>
      <ul v-if="state.plan.blockers.length" class="error-message"><li v-for="blocker in state.plan.blockers" :key="blocker">{{ blocker }}</li></ul>
      <p class="muted">{{ text.incusRefreshNotice }}</p>
      <label class="checkbox-line"><input v-model="approved" type="checkbox" :disabled="busy || state.plan.blockers.length !== 0" />{{ text.incusApproveDisplayed }}</label>
      <button type="button" class="primary-button" :disabled="disabled || busy || !approved || state.plan.blockers.length !== 0" @click="apply">{{ text.incusApplyRun }}</button>
    </div>
    <p v-if="state.status === 'submitted'" role="status">{{ text.maintenanceJobQueued }} <code>{{ state.jobID }}</code></p>
    </template>
    <div v-if="imagePruneAvailable" class="plan-preview">
      <h4>{{ text.incusImagePruneTitle }}</h4>
      <button type="button" class="secondary-button" :disabled="disabled || pruneBusy || !workspace" @click="preparePrune">{{ text.incusImagePrunePlanRun }}</button>
      <p v-if="pruneBusy" role="status">{{ text.incusPlanning }}</p>
      <p v-if="pruneError" class="error-message" role="alert">{{ problemMessage(locale, pruneError) }}</p>
      <div v-if="prunePlan">
        <p><code>{{ prunePlanJob }}</code></p>
        <p>{{ text.incusImagePruneDelete }}: {{ prunePlan.delete?.length ?? 0 }} / {{ text.incusImagePruneRetain }}: {{ prunePlan.retained?.length ?? 0 }}</p>
        <ul><li v-for="target in prunePlan.delete ?? []" :key="target.project + target.fingerprint"><code>{{ target.project }}</code> <code>{{ target.fingerprint }}</code></li></ul>
        <p v-if="(prunePlan.delete?.length ?? 0) === 0">{{ text.incusNoChanges }}</p>
        <details v-if="prunePlan.retained.length"><summary>{{ text.incusImagePruneRetain }}: {{ prunePlan.retained.length }}</summary>
          <ul><li v-for="item in prunePlan.retained" :key="item.project + item.fingerprint"><code>{{ item.project }}</code> <code>{{ item.fingerprint }}</code> — {{ item.reasons.join(", ") }}</li></ul>
        </details>
        <p v-if="prunePlan.blockers.length" class="error-message">{{ text.incusImagePruneStopConsumers }}</p>
        <p class="muted">{{ text.incusRefreshNotice }}</p>
        <label class="checkbox-line"><input v-model="pruneApproved" type="checkbox" :disabled="pruneBusy || prunePlan.blockers.length !== 0 || prunePlan.delete.length === 0" />{{ text.incusApproveDisplayed }}</label>
        <button type="button" class="primary-button" :disabled="disabled || pruneBusy || !pruneApproved || prunePlan.blockers.length !== 0 || prunePlan.delete.length === 0" @click="applyPrune">{{ text.incusApplyRun }}</button>
      </div>
      <p v-if="pruneState.status === 'submitted'" role="status">{{ text.maintenanceJobQueued }} <code>{{ pruneState.jobID }}</code></p>
    </div>
  </div>
</template>
