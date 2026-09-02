<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-070 CONSOLE-R-087 CONSOLE-R-088 CONSOLE-R-100 CONSOLE-R-101 CONSOLE-R-103 CONSOLE-R-104 CONSOLE-R-106 CONSOLE-R-114 CONSOLE-R-122 CONSOLE-R-123 CONSOLE-R-125 CONSOLE-R-126 CONSOLE-R-127 CONSOLE-R-129 CONSOLE-R-130 CONSOLE-R-131 CONSOLE-R-133 CONSOLE-R-150
import { computed, onMounted, ref } from "vue"

import {
  createInitialOwner,
  enrollmentCSRFCookieName,
  exchangeBootstrapToken,
  issueEnrollmentHandoff,
  issuePreAuthCSRF,
  loginLocalOwner,
  readCookie,
  refreshAuthSession,
  submitEnrollmentHandoff,
} from "./api/auth"
import { api } from "./api/client"
import { APIProblemError, problemMessage } from "./api/problems"
import type { components } from "./api/schema"
import { initialPhase, type EntryPhase } from "./auth/flow"
import WorkspaceConfig from "./config/WorkspaceConfig.vue"
import WorkspaceDeployment from "./deployment/WorkspaceDeployment.vue"
import { initialLocale, messages, type Locale } from "./i18n/messages"
import WorkspaceJobs from "./jobs/WorkspaceJobs.vue"
import WorkspaceLifecycle from "./lifecycle/WorkspaceLifecycle.vue"
import WorkspaceMaintenance from "./maintenance/WorkspaceMaintenance.vue"
import WorkspaceModules from "./modules/WorkspaceModules.vue"

type SystemResponse = components["schemas"]["SystemResponse"]
type Phase = EntryPhase | "bootstrap-ready" | "authenticated" | "proxy-auth"

const locale = ref<Locale>(initialLocale(navigator.languages))
const status = ref<"loading" | "ready" | "unavailable">("loading")
const system = ref<SystemResponse | null>(null)
const phase = ref<Phase>("m0")
const busy = ref(false)
const errorCode = ref<string | null>(null)
const bootstrapToken = ref("")
const password = ref("")
const passwordConfirmation = ref("")
const ownerCreated = ref(false)
const sessionCSRF = ref("")
const configRevision = ref(0)
const jobsRevision = ref(0)
const insecureTransport = window.location.protocol !== "https:"
const text = computed(() => messages[locale.value])
const errorText = computed(() => (errorCode.value === null ? "" : problemMessage(locale.value, errorCode.value)))
const canDownloadCA = computed(() => {
  const issuer = system.value?.certificate_issuer
  return (
    (issuer === "internal" || issuer === "acme") &&
    (system.value?.console_state === "enrollment" || phase.value === "authenticated")
  )
})
const canConfigure = computed(
  () =>
    sessionCSRF.value !== "" &&
    (phase.value === "bootstrap-ready" || phase.value === "authenticated") &&
    (system.value?.workspace_ids.length ?? 0) > 0,
)
const canRecoverJobs = computed(
  () =>
    status.value === "ready" &&
    system.value !== null &&
    system.value.console_state !== "m0" &&
    system.value.workspace_ids.length > 0,
)
const deploymentConsoleState = computed<"bootstrap" | "full">(() =>
  phase.value === "authenticated" ? "full" : "bootstrap",
)

function toggleLocale() {
  locale.value = locale.value === "zh" ? "en" : "zh"
  document.documentElement.lang = locale.value === "zh" ? "zh-CN" : "en"
}

document.documentElement.lang = locale.value === "zh" ? "zh-CN" : "en"

function showError(error: unknown) {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

async function submitBootstrapToken() {
  if (busy.value || bootstrapToken.value === "") return
  busy.value = true
  errorCode.value = null
  const token = bootstrapToken.value
  bootstrapToken.value = ""
  try {
    const csrf = await issuePreAuthCSRF()
    const session = await exchangeBootstrapToken(token, csrf)
    if (session.state === "enrollment") {
      const handoff = await issueEnrollmentHandoff(session.csrf_token)
      submitEnrollmentHandoff(document, handoff)
      return
    }
    sessionCSRF.value = session.csrf_token
    phase.value = "bootstrap-ready"
    jobsRevision.value += 1
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function submitOwner() {
  if (busy.value || password.value === "") return
  errorCode.value = null
  if (password.value !== passwordConfirmation.value) {
    errorCode.value = "password_mismatch"
    password.value = ""
    passwordConfirmation.value = ""
    return
  }
  const ownerPassword = password.value
  password.value = ""
  passwordConfirmation.value = ""
  const csrf = readCookie(enrollmentCSRFCookieName)
  if (!csrf) {
    errorCode.value = "enrollment_session_missing"
    phase.value = "enrollment-recovery"
    return
  }
  busy.value = true
  try {
    await createInitialOwner(ownerPassword, csrf)
    ownerCreated.value = true
    phase.value = "login"
    if (system.value) system.value = { ...system.value, console_state: "full" }
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

async function submitLogin() {
  if (busy.value || password.value === "") return
  busy.value = true
  errorCode.value = null
  const localPassword = password.value
  password.value = ""
  try {
    const csrf = await issuePreAuthCSRF()
    const session = await loginLocalOwner(localPassword, csrf)
    sessionCSRF.value = session.csrf_token
    phase.value = "authenticated"
    jobsRevision.value += 1
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

onMounted(async () => {
  try {
    const { data, error } = await api.GET("/api/v1/system")
    if (error || !data) {
      status.value = "unavailable"
      return
    }
    system.value = data
    phase.value = data.console_state === "full" && data.listener === "trusted_proxy"
      ? "proxy-auth"
      : initialPhase(data.console_state, readCookie(enrollmentCSRFCookieName) !== null)
    status.value = "ready"
    if (data.console_state === "bootstrap" || data.console_state === "full" || data.console_state === "enrollment") {
      try {
        const session = await refreshAuthSession()
        if (session.state === "bootstrap") {
          sessionCSRF.value = session.csrf_token
          phase.value = "bootstrap-ready"
        } else if (session.state === "full") {
          sessionCSRF.value = session.csrf_token
          phase.value = "authenticated"
        } else if (session.state === "enrollment") {
          const handoff = await issueEnrollmentHandoff(session.csrf_token)
          submitEnrollmentHandoff(document, handoff)
        }
      } catch (error) {
        if (data.listener === "trusted_proxy") phase.value = "proxy-auth"
        if (!(error instanceof APIProblemError && (error.code === "unauthenticated" || error.code === "forbidden"))) {
          showError(error)
        }
      }
    }
  } catch {
    status.value = "unavailable"
  }
})
</script>

<template>
  <div class="app-shell">
    <aside v-if="insecureTransport" class="lan-warning" role="alert" data-lan-risk-banner>
      <strong>{{ text.lanRiskTitle }}</strong>
      <span>{{ text.lanRisk }}</span>
    </aside>

    <header class="app-header">
      <div>
        <p class="eyebrow">ANAS</p>
        <h1>{{ text.product }}</h1>
      </div>
      <button type="button" class="language-button" @click="toggleLocale">{{ text.language }}</button>
    </header>

    <main>
      <section class="status-card" aria-live="polite">
        <h2>{{ text[status] }}</h2>
        <dl v-if="status === 'ready'">
          <div>
            <dt>{{ text.runtimeState }}</dt>
            <dd>{{ system?.console_state || "—" }}</dd>
          </div>
          <div>
            <dt>{{ text.certificate }}</dt>
            <dd>{{ system?.certificate_issuer || "—" }}</dd>
          </div>
          <div>
            <dt>{{ text.workspaces }}</dt>
            <dd>{{ system?.workspace_ids.length ?? 0 }}</dd>
          </div>
        </dl>
        <p v-if="status === 'ready' && !system?.workspace_ids.length" class="next-step">
          {{ text.noWorkspaces }} <code>anas init</code>
        </p>
      </section>

      <section v-if="status === 'ready'" class="auth-card" aria-live="polite">
        <div v-if="phase === 'm0'">
          <h2>{{ text.m0Title }}</h2>
          <p>{{ text.m0Help }}</p>
        </div>

        <form
          v-else-if="phase === 'bootstrap' || phase === 'enrollment-recovery'"
          class="auth-form"
          @submit.prevent="submitBootstrapToken"
        >
          <h2>{{ phase === "bootstrap" ? text.bootstrapTitle : text.enrollmentRecoveryTitle }}</h2>
          <p>{{ phase === "bootstrap" ? text.bootstrapHelp : text.enrollmentRecoveryHelp }}</p>
          <label for="bootstrap-token">{{ text.bootstrapToken }}</label>
          <input
            id="bootstrap-token"
            v-model="bootstrapToken"
            name="bootstrap-token"
            type="password"
            autocomplete="one-time-code"
            spellcheck="false"
            required
            :disabled="busy"
          />
          <button type="submit" class="primary-button" :disabled="busy || bootstrapToken === ''">
            {{ busy ? text.working : text.continueAction }}
          </button>
        </form>

        <div v-else-if="phase === 'bootstrap-ready'">
          <h2>{{ text.bootstrapReadyTitle }}</h2>
          <p>{{ text.bootstrapReadyHelp }}</p>
        </div>

        <form v-else-if="phase === 'owner'" class="auth-form" @submit.prevent="submitOwner">
          <h2>{{ text.ownerTitle }}</h2>
          <p>{{ text.ownerHelp }}</p>
          <label for="owner-password">{{ text.newPassword }}</label>
          <input
            id="owner-password"
            v-model="password"
            name="owner-password"
            type="password"
            autocomplete="new-password"
            required
            :disabled="busy"
          />
          <label for="owner-password-confirmation">{{ text.confirmPassword }}</label>
          <input
            id="owner-password-confirmation"
            v-model="passwordConfirmation"
            name="owner-password-confirmation"
            type="password"
            autocomplete="new-password"
            required
            :disabled="busy"
          />
          <button type="submit" class="primary-button" :disabled="busy || password === ''">
            {{ busy ? text.working : text.createOwner }}
          </button>
        </form>

        <form v-else-if="phase === 'login'" class="auth-form" @submit.prevent="submitLogin">
          <h2>{{ text.loginTitle }}</h2>
          <p>{{ text.loginHelp }}</p>
          <aside v-if="ownerCreated" class="security-reminder" role="status">
            <strong>{{ text.dnsRotationTitle }}</strong>
            <span>{{ text.dnsRotation }}</span>
          </aside>
          <label for="local-password">{{ text.password }}</label>
          <input
            id="local-password"
            v-model="password"
            name="local-password"
            type="password"
            autocomplete="current-password"
            required
            :disabled="busy"
          />
          <button type="submit" class="primary-button" :disabled="busy || password === ''">
            {{ busy ? text.working : text.login }}
          </button>
        </form>

        <div v-else-if="phase === 'authenticated'">
          <h2>{{ system?.listener === "trusted_proxy" ? text.proxySignedInTitle : text.signedInTitle }}</h2>
          <p>{{ system?.listener === "trusted_proxy" ? text.proxySignedInHelp : text.signedInHelp }}</p>
        </div>

        <div v-else-if="phase === 'proxy-auth'">
          <h2>{{ text.proxyAuthTitle }}</h2>
          <p>{{ text.proxyAuthHelp }}</p>
        </div>

        <p v-if="errorCode" class="error-message" role="alert">
          <strong>{{ text.errorTitle }}</strong> {{ errorText }}
        </p>
      </section>

      <WorkspaceConfig
        v-if="canConfigure && system"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        @saved="configRevision += 1"
      />

      <WorkspaceDeployment
        v-if="canConfigure && system"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :console-state="deploymentConsoleState"
        :authentication-source="system.listener === 'trusted_proxy' ? 'oidc_proxy' : 'local'"
        :config-revision="configRevision"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceLifecycle
        v-if="phase === 'authenticated' && system"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceModules
        v-if="phase === 'authenticated' && system"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :refresh-revision="configRevision"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceMaintenance
        v-if="phase === 'authenticated' && system"
        :workspace-ids="system.workspace_ids"
        :backup-target-ids="system.backup_target_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :authentication-source="system.listener === 'trusted_proxy' ? 'oidc_proxy' : 'local'"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceJobs
        v-if="canRecoverJobs"
        :locale="locale"
        :refresh-revision="jobsRevision"
      />

      <section class="config-card" aria-labelledby="certificate-access-title">
        <div class="config-heading">
          <div>
            <p class="eyebrow">M1C</p>
            <h2 id="certificate-access-title">{{ text.accessTitle }}</h2>
            <p>{{ text.accessHelp }}</p>
          </div>
        </div>
        <dl>
          <div>
            <dt>{{ text.certificate }}</dt>
            <dd>{{ system?.certificate_issuer || "—" }}</dd>
          </div>
          <div v-if="system?.direct_recovery_urls.length">
            <dt>{{ text.directRecovery }}</dt>
            <dd>
              <ul class="access-origins" data-direct-recovery-origins>
                <li v-for="origin in system.direct_recovery_urls" :key="origin"><code>{{ origin }}</code></li>
              </ul>
            </dd>
          </div>
          <div v-if="system?.proxy_url">
            <dt>{{ text.proxyEntry }}</dt>
            <dd><code>{{ system.proxy_url }}</code></dd>
          </div>
        </dl>
        <nav class="utility-links" :aria-label="text.utilities">
          <a v-if="canDownloadCA" href="/api/v1/system/ca" download="anas-internal-ca.crt">{{ text.caDownload }}</a>
          <a v-if="system?.listener !== 'trusted_proxy'" href="/emergency">{{ text.emergency }}</a>
        </nav>
      </section>
    </main>
  </div>
</template>
