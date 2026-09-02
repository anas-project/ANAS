<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-047 CONSOLE-R-123 CONSOLE-R-127 CONSOLE-R-132 CONSOLE-R-133 CONSOLE-R-144 CONSOLE-R-149 CONSOLE-R-150
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue"

import { newIdempotencyKey } from "../api/deployment"
import {
  changeSnapshot,
  createSnapshot,
  issueLocalAdminRevealStepUp,
  listBackups,
  listLocalAdmins,
  listSnapshots,
  planBackup,
  previewTerminalAction,
  revealLocalAdmin,
  rotateLocalAdmin,
  type BackupPlanRequest,
  type BackupPlanResponse,
  type BackupRecord,
  type LocalAdminRecord,
  type LocalAdminReveal,
  type SnapshotRecord,
  type TerminalActionDescriptor,
  type TerminalActionRequest,
} from "../api/maintenance"
import { APIProblemError, problemMessage } from "../api/problems"
import { messages, type Locale } from "../i18n/messages"
import { beginCredentialExposure } from "./model"

type BackupMode = NonNullable<BackupPlanRequest["mode"]>

const props = defineProps<{
  workspaceIds: string[]
  backupTargetIds: string[]
  csrf: string
  locale: Locale
  authenticationSource: "local" | "oidc_proxy"
}>()
const emit = defineEmits<{ jobCreated: [jobID: string] }>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const selectedBackupTarget = ref(props.backupTargetIds[0] ?? "")
const snapshots = ref<SnapshotRecord[]>([])
const backups = ref<BackupRecord[]>([])
const admins = ref<LocalAdminRecord[]>([])
const snapshotLabel = ref("")
const includeSnapshotUserData = ref(false)
const restoreSnapshotUserData = ref(false)
const forceSnapshotDelete = ref(false)
const backupMode = ref<BackupMode>("copy")
const backupNoStop = ref(false)
const backupSkipUserData = ref(false)
const backupPlan = ref<BackupPlanResponse | null>(null)
const descriptor = ref<TerminalActionDescriptor | null>(null)
const ownerPassword = ref("")
const revealed = ref<LocalAdminReveal | null>(null)
const queuedJobID = ref("")
const busy = ref(false)
const loading = ref(false)
const errorCode = ref<string | null>(null)
let stopCredentialExposure: (() => void) | null = null

const text = computed(() => messages[props.locale])
const errorText = computed(() => errorCode.value === null ? "" : problemMessage(props.locale, errorCode.value))
const localAuthentication = computed(() => props.authenticationSource === "local")
const descriptorTarget = computed(() => {
  const target = descriptor.value?.target
  if (!target) return "—"
  return target.snapshot_id ?? target.backup_id ?? target.backup_plan_id ?? target.backup_target_id ?? "—"
})

function showError(error: unknown): void {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

function clearCredential(): void {
  if (revealed.value !== null) revealed.value.password = ""
  revealed.value = null
  ownerPassword.value = ""
  stopCredentialExposure?.()
  stopCredentialExposure = null
}

function clearSensitiveViews(): void {
  clearCredential()
  descriptor.value = null
  backupPlan.value = null
}

async function load(): Promise<void> {
  if (selectedWorkspace.value === "" || loading.value) return
  loading.value = true
  errorCode.value = null
  try {
    const [snapshotResult, adminResult] = await Promise.all([
      listSnapshots(selectedWorkspace.value),
      listLocalAdmins(selectedWorkspace.value),
    ])
    if (snapshotResult.workspace_id !== selectedWorkspace.value || adminResult.workspace_id !== selectedWorkspace.value) {
      throw new APIProblemError({ code: "maintenance_response_invalid" })
    }
    snapshots.value = snapshotResult.snapshots
    admins.value = adminResult.accounts
    await loadBackupList()
  } catch (error) {
    snapshots.value = []
    admins.value = []
    backups.value = []
    showError(error)
  } finally {
    loading.value = false
  }
}

async function loadBackupList(): Promise<void> {
  backups.value = []
  if (selectedBackupTarget.value === "") return
  const response = await listBackups(selectedWorkspace.value, selectedBackupTarget.value)
  if (response.workspace_id !== selectedWorkspace.value || response.target_id !== selectedBackupTarget.value) {
    throw new APIProblemError({ code: "maintenance_response_invalid" })
  }
  backups.value = response.backups
}

function recordJob(jobID: string): void {
  queuedJobID.value = jobID
  emit("jobCreated", jobID)
}

async function queueSnapshotCreate(): Promise<void> {
  if (busy.value) return
  busy.value = true
  errorCode.value = null
  try {
    const response = await createSnapshot(selectedWorkspace.value, snapshotLabel.value, includeSnapshotUserData.value, props.csrf, newIdempotencyKey())
    snapshotLabel.value = ""
    includeSnapshotUserData.value = false
    recordJob(response.job.id)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function queueSnapshotAction(snapshot: SnapshotRecord, action: "pin" | "unpin" | "verify"): Promise<void> {
  if (busy.value) return
  busy.value = true
  errorCode.value = null
  try {
    const response = await changeSnapshot(selectedWorkspace.value, snapshot.id, action, snapshot.label, props.csrf, newIdempotencyKey())
    recordJob(response.job.id)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function calculateBackupPlan(): Promise<void> {
  if (busy.value || selectedBackupTarget.value === "") return
  busy.value = true
  descriptor.value = null
  backupPlan.value = null
  errorCode.value = null
  const request: BackupPlanRequest = {
    target_id: selectedBackupTarget.value,
    mode: backupMode.value,
    no_stop: backupNoStop.value,
    skip_userdata: backupSkipUserData.value,
  }
  try {
    const response = await planBackup(selectedWorkspace.value, request, props.csrf)
    if (response.workspace_id !== selectedWorkspace.value || response.target_id !== selectedBackupTarget.value) {
      throw new APIProblemError({ code: "maintenance_response_invalid" })
    }
    backupPlan.value = response
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function showTerminalDescriptor(request: TerminalActionRequest): Promise<void> {
  if (busy.value) return
  busy.value = true
  descriptor.value = null
  errorCode.value = null
  try {
    const response = await previewTerminalAction(selectedWorkspace.value, request, props.csrf)
    if (response.workspace_id !== selectedWorkspace.value || response.operation !== request.operation) {
      throw new APIProblemError({ code: "maintenance_response_invalid" })
    }
    descriptor.value = response
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

function showSnapshotRestore(snapshot: SnapshotRecord): void {
  void showTerminalDescriptor({ operation: "snapshot.restore", snapshot: { id: snapshot.id, restore_userdata: restoreSnapshotUserData.value } })
}

function showSnapshotDelete(snapshot: SnapshotRecord): void {
  void showTerminalDescriptor({ operation: "snapshot.delete", snapshot: { id: snapshot.id, force: forceSnapshotDelete.value } })
}

function showBackupCreate(): void {
  const planned = backupPlan.value
  if (planned === null) return
  void showTerminalDescriptor({
    operation: "backup.create",
    backup: {
      target_id: planned.target_id,
      plan_id: planned.plan_id,
      mode: backupMode.value,
      no_stop: backupNoStop.value,
      skip_userdata: backupSkipUserData.value,
    },
  })
}

function showBackupAction(backup: BackupRecord, operation: "backup.restore" | "backup.verify"): void {
  void showTerminalDescriptor({ operation, backup: { target_id: selectedBackupTarget.value, backup_id: backup.id } })
}

async function rotate(account: LocalAdminRecord): Promise<void> {
  if (busy.value) return
  clearCredential()
  busy.value = true
  errorCode.value = null
  try {
    const response = await rotateLocalAdmin(selectedWorkspace.value, account, props.csrf, newIdempotencyKey())
    recordJob(response.job.id)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function reveal(account: LocalAdminRecord): Promise<void> {
  if (busy.value || (localAuthentication.value && ownerPassword.value === "")) return
  const password = ownerPassword.value
  ownerPassword.value = ""
  clearCredential()
  busy.value = true
  errorCode.value = null
  let proof = ""
  try {
    const stepUp = await issueLocalAdminRevealStepUp(
      selectedWorkspace.value,
      account.target_id,
      localAuthentication.value ? password : undefined,
      props.csrf,
    )
    proof = stepUp.proof
    const response = await revealLocalAdmin(selectedWorkspace.value, account, proof, props.csrf)
    proof = ""
    revealed.value = response
    stopCredentialExposure = beginCredentialExposure(clearCredential)
  } catch (error) {
    proof = ""
    showError(error)
  } finally {
    busy.value = false
  }
}

watch(() => props.workspaceIds, (ids) => {
  if (!ids.includes(selectedWorkspace.value)) selectedWorkspace.value = ids[0] ?? ""
})
watch(() => props.backupTargetIds, (ids) => {
  if (!ids.includes(selectedBackupTarget.value)) selectedBackupTarget.value = ids[0] ?? ""
})
watch(selectedWorkspace, () => {
  clearSensitiveViews()
  void load()
})
watch(selectedBackupTarget, () => {
  descriptor.value = null
  backupPlan.value = null
  void loadBackupList().catch(showError)
})
onMounted(() => void load())
onBeforeUnmount(clearCredential)
</script>

<template>
  <section class="deployment-card maintenance-card" aria-live="polite" data-workspace-maintenance>
    <div class="config-heading">
      <div>
        <p class="eyebrow">M4</p>
        <h2>{{ text.maintenanceTitle }}</h2>
        <p>{{ text.maintenanceHelp }}</p>
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
        {{ loading ? text.maintenanceLoading : text.reloadConfig }}
      </button>
    </div>
    <p v-if="errorCode" class="error-message" role="alert"><strong>{{ text.errorTitle }}</strong> {{ errorText }}</p>
    <p v-if="queuedJobID" role="status">{{ text.maintenanceJobQueued }} <code>{{ queuedJobID }}</code></p>

    <section class="maintenance-section">
      <h3>{{ text.snapshotTitle }}</h3>
      <p class="muted">{{ text.snapshotHelp }}</p>
      <div class="maintenance-form-row">
        <label>{{ text.snapshotLabel }}<input v-model="snapshotLabel" type="text" :disabled="busy" /></label>
        <label class="checkbox-line"><input v-model="includeSnapshotUserData" type="checkbox" :disabled="busy" /> {{ text.snapshotIncludeUserData }}</label>
        <button type="button" class="primary-button" :disabled="busy" @click="queueSnapshotCreate">{{ text.snapshotCreate }}</button>
      </div>
      <div class="maintenance-form-row">
        <label class="checkbox-line"><input v-model="restoreSnapshotUserData" type="checkbox" /> {{ text.snapshotRestoreUserData }}</label>
        <label class="checkbox-line"><input v-model="forceSnapshotDelete" type="checkbox" /> {{ text.snapshotForceDelete }}</label>
      </div>
      <ul class="maintenance-list">
        <li v-for="snapshot in snapshots" :key="snapshot.id">
          <div><strong>{{ snapshot.id }}</strong> · {{ snapshot.kind }} · {{ snapshot.healthy ? text.healthy : text.unhealthy }} · {{ snapshot.includes_userdata ? text.userdataIncluded : text.userdataExcluded }}</div>
          <div class="config-actions">
            <button type="button" class="text-button" :disabled="busy" @click="queueSnapshotAction(snapshot, snapshot.pinned ? 'unpin' : 'pin')">{{ snapshot.pinned ? text.snapshotUnpin : text.snapshotPin }}</button>
            <button type="button" class="text-button" :disabled="busy" @click="queueSnapshotAction(snapshot, 'verify')">{{ text.verify }}</button>
            <button type="button" class="text-button" :disabled="busy" @click="showSnapshotRestore(snapshot)">{{ text.showRestoreCommand }}</button>
            <button type="button" class="text-button" :disabled="busy" @click="showSnapshotDelete(snapshot)">{{ text.showDeleteCommand }}</button>
          </div>
        </li>
      </ul>
    </section>

    <section class="maintenance-section">
      <h3>{{ text.backupTitle }}</h3>
      <p class="muted">{{ text.backupHelp }}</p>
      <p v-if="backupTargetIds.length === 0" class="draft-notice">{{ text.backupNoTargets }}</p>
      <div v-else class="maintenance-form-row">
        <label>{{ text.backupTarget }}
          <select v-model="selectedBackupTarget" :disabled="busy">
            <option v-for="target in backupTargetIds" :key="target" :value="target">{{ target }}</option>
          </select>
        </label>
        <label>{{ text.backupMode }}
          <select v-model="backupMode" :disabled="busy">
            <option value="copy">copy</option><option value="snapshot">snapshot</option><option value="send">send</option><option value="send-file">send-file</option>
          </select>
        </label>
        <label class="checkbox-line"><input v-model="backupNoStop" type="checkbox" :disabled="busy" /> {{ text.backupNoStop }}</label>
        <label class="checkbox-line"><input v-model="backupSkipUserData" type="checkbox" :disabled="busy" /> {{ text.backupSkipUserData }}</label>
        <button type="button" class="secondary-button" :disabled="busy" @click="calculateBackupPlan">{{ text.backupPlan }}</button>
      </div>
      <article v-if="backupPlan" class="plan-preview">
        <h4>{{ text.backupPlanResult }}</h4>
        <p>{{ text.backupRecommended }} <strong>{{ backupPlan.capabilities.recommended || text.none }}</strong> · {{ text.backupTransfer }} {{ backupPlan.plan.transfer_bytes }}</p>
        <p>{{ text.backupIncludes }} {{ backupPlan.plan.includes.join(", ") }}</p>
        <button type="button" class="secondary-button" :disabled="busy" @click="showBackupCreate">{{ text.showBackupCreateCommand }}</button>
      </article>
      <ul class="maintenance-list">
        <li v-for="backup in backups" :key="backup.id">
          <div><strong>{{ backup.id }}</strong> · {{ backup.mode }} · {{ backup.complete ? text.complete : text.incomplete }} · {{ backup.includes_userdata ? text.userdataIncluded : text.userdataExcluded }}</div>
          <div class="config-actions">
            <button type="button" class="text-button" :disabled="busy" @click="showBackupAction(backup, 'backup.restore')">{{ text.showRestoreCommand }}</button>
            <button type="button" class="text-button" :disabled="busy" @click="showBackupAction(backup, 'backup.verify')">{{ text.showVerifyCommand }}</button>
          </div>
        </li>
      </ul>
    </section>

    <section class="maintenance-section">
      <h3>{{ text.localAdminTitle }}</h3>
      <p class="muted">{{ text.localAdminHelp }}</p>
      <label v-if="localAuthentication" class="step-up-form">{{ text.stepUpPassword }}
        <input v-model="ownerPassword" type="password" autocomplete="current-password" :disabled="busy" />
      </label>
      <p v-else class="muted">{{ text.proxyStepUpHelp }}</p>
      <ul class="maintenance-list">
        <li v-for="account in admins" :key="account.target_id">
          <div><strong>{{ account.module }} / {{ account.account }}</strong> · {{ account.username }} · {{ account.purpose }}</div>
          <div class="config-actions">
            <button type="button" class="text-button" :disabled="busy" @click="rotate(account)">{{ text.localAdminRotate }}</button>
            <button type="button" class="text-button" :disabled="busy || (localAuthentication && ownerPassword === '')" @click="reveal(account)">{{ text.localAdminReveal }}</button>
          </div>
        </li>
      </ul>
      <aside v-if="revealed" class="credential-reveal" role="status" data-credential-reveal>
        <strong>{{ revealed.module }} / {{ revealed.account }}</strong>
        <span>{{ text.username }} <code>{{ revealed.username }}</code></span>
        <span>{{ text.password }} <code>{{ revealed.password }}</code></span>
        <button type="button" class="text-button" @click="clearCredential">{{ text.clearCredential }}</button>
      </aside>
    </section>

    <section v-if="descriptor" class="terminal-descriptor" data-terminal-descriptor>
      <h3>{{ text.terminalCommandTitle }}</h3>
      <p class="muted">{{ text.terminalCommandHelp }}</p>
      <dl>
        <div><dt>{{ text.workspace }}</dt><dd>{{ descriptor.workspace_id }}</dd></div>
        <div><dt>{{ text.target }}</dt><dd>{{ descriptorTarget }}</dd></div>
        <div><dt>data/</dt><dd>{{ descriptor.impact.data ? text.yes : text.no }}</dd></div>
        <div><dt>userdata/</dt><dd>{{ descriptor.impact.userdata ? text.yes : text.no }}</dd></div>
        <div><dt>{{ text.reversible }}</dt><dd>{{ descriptor.impact.reversible ? text.yes : text.no }}</dd></div>
      </dl>
      <pre><code>{{ descriptor.display }}</code></pre>
      <p>{{ text.cliContract }} <code>{{ descriptor.cli_contract }}</code></p>
      <details><summary>{{ text.argvTokens }}</summary><ol><li v-for="(token, index) in descriptor.argv" :key="index"><code>{{ token }}</code></li></ol></details>
    </section>
  </section>
</template>
