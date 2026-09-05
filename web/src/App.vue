<script setup lang="ts">
// REQUIREMENTS: CONSOLE-R-070 CONSOLE-R-087 CONSOLE-R-088 CONSOLE-R-100 CONSOLE-R-101 CONSOLE-R-103 CONSOLE-R-104 CONSOLE-R-106 CONSOLE-R-114 CONSOLE-R-122 CONSOLE-R-123 CONSOLE-R-125 CONSOLE-R-126 CONSOLE-R-127 CONSOLE-R-129 CONSOLE-R-130 CONSOLE-R-131 CONSOLE-R-133 CONSOLE-R-150 CONSOLE-R-185 CONSOLE-R-186
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue"

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
import {
  clockSkewMs,
  formatCountdown,
  observeSession,
  remainingMs,
  sessionLifetime,
  withActivity,
  type SessionInstants,
  type SessionLifetime,
} from "./api/session"
import { initialPhase, type EntryPhase } from "./auth/flow"
import WorkspaceConfig from "./config/WorkspaceConfig.vue"
import WorkspaceDeployment from "./deployment/WorkspaceDeployment.vue"
import { initialLocale, messages, type Locale } from "./i18n/messages"
import WorkspaceJobs from "./jobs/WorkspaceJobs.vue"
import WorkspaceLifecycle from "./lifecycle/WorkspaceLifecycle.vue"
import WorkspaceMaintenance from "./maintenance/WorkspaceMaintenance.vue"
import WorkspaceModules from "./modules/WorkspaceModules.vue"
import WorkspaceAudit from "./audit/WorkspaceAudit.vue"
import { sectionFromLocation, sectionHref, visibleSections, type ConsoleSection } from "./nav"

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
const sessionExpiry = ref<SessionLifetime | null>(null)
const clockTick = ref(Date.now())
const configRevision = ref(0)
const jobsRevision = ref(0)
const insecureTransport = window.location.protocol !== "https:"
// Warning window before the session ends. Long enough to finish a sentence in
// a form, short enough that a banner is not permanently on screen.
const sessionWarningMs = 5 * 60 * 1000
const text = computed(() => messages[locale.value])
const errorText = computed(() => (errorCode.value === null ? "" : problemMessage(locale.value, errorCode.value)))
const sessionRemaining = computed(() =>
  sessionExpiry.value === null ? null : remainingMs(sessionExpiry.value, clockTick.value),
)
const sessionExpiring = computed(
  () => sessionRemaining.value !== null && sessionRemaining.value <= sessionWarningMs,
)
const sessionCountdown = computed(() => formatCountdown(sessionRemaining.value ?? 0))
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

const section = ref<ConsoleSection>(sectionFromLocation(window.location.hash))
const sections = computed(() =>
  visibleSections({
    canConfigure: canConfigure.value,
    authenticated: phase.value === "authenticated",
    canRecoverJobs: canRecoverJobs.value,
  }),
)

function syncSectionFromHash() {
  section.value = sectionFromLocation(window.location.hash)
}

window.addEventListener("hashchange", syncSectionFromHash)
onBeforeUnmount(() => window.removeEventListener("hashchange", syncSectionFromHash))

// Signing out, or losing a session, must not strand the operator on a page
// whose routes they can no longer reach.
watch(sections, (available) => {
  if (!available.includes(section.value)) section.value = "overview"
})

function toggleLocale() {
  locale.value = locale.value === "zh" ? "en" : "zh"
  document.documentElement.lang = locale.value === "zh" ? "zh-CN" : "en"
}

document.documentElement.lang = locale.value === "zh" ? "zh-CN" : "en"

function showError(error: unknown) {
  errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
}

// The screen an unauthenticated visitor belongs on is decided by the state the
// daemon reported at load, so entry after a load and re-entry after a session
// ends resolve it the same way instead of drifting apart.
function entryPhase(): Phase {
  const data = system.value
  if (data === null) return "m0"
  return data.console_state === "full" && data.listener === "trusted_proxy"
    ? "proxy-auth"
    : initialPhase(data.console_state, readCookie(enrollmentCSRFCookieName) !== null)
}

function adoptSession(session: SessionInstants) {
  clockTick.value = Date.now()
  sessionExpiry.value = sessionLifetime(session, clockSkewMs(), clockTick.value)
}

// A session can end without any request failing — the idle window simply runs
// out — and can be rejected mid-page by any route, so both paths land here and
// leave the console in the state a fresh visitor would find.
function endSession() {
  if (sessionExpiry.value === null && sessionCSRF.value === "") return
  sessionExpiry.value = null
  sessionCSRF.value = ""
  password.value = ""
  passwordConfirmation.value = ""
  bootstrapToken.value = ""
  busy.value = false
  errorCode.value = "unauthenticated"
  phase.value = entryPhase()
  jobsRevision.value += 1
}

// Extending is an explicit click rather than a poll: the session route is
// itself an authenticated request, so refreshing the countdown on a timer
// would keep sliding the idle window and the session would never time out.
async function extendSession() {
  if (busy.value) return
  busy.value = true
  errorCode.value = null
  try {
    const session = await refreshAuthSession()
    sessionCSRF.value = session.csrf_token
    adoptSession(session)
  } catch (error) {
    showError(error)
  } finally {
    busy.value = false
  }
}

let sessionTicker: ReturnType<typeof setInterval> | null = null

function stopSessionTicker() {
  if (sessionTicker === null) return
  clearInterval(sessionTicker)
  sessionTicker = null
}

watch(sessionExpiry, (lifetime) => {
  if (lifetime === null) {
    stopSessionTicker()
    return
  }
  if (sessionTicker !== null) return
  sessionTicker = setInterval(() => {
    clockTick.value = Date.now()
    if (sessionExpiry.value !== null && remainingMs(sessionExpiry.value, clockTick.value) === 0) endSession()
  }, 1000)
})

const stopObservingSession = observeSession((signal) => {
  if (signal.kind === "activity") {
    if (sessionExpiry.value !== null) sessionExpiry.value = withActivity(sessionExpiry.value, signal.at)
    return
  }
  endSession()
})

onBeforeUnmount(() => {
  stopObservingSession()
  stopSessionTicker()
})

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
    adoptSession(session)
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
    adoptSession(session)
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
    phase.value = entryPhase()
    status.value = "ready"
    if (data.console_state === "bootstrap" || data.console_state === "full" || data.console_state === "enrollment") {
      try {
        const session = await refreshAuthSession()
        if (session.state === "bootstrap") {
          sessionCSRF.value = session.csrf_token
          adoptSession(session)
          phase.value = "bootstrap-ready"
        } else if (session.state === "full") {
          sessionCSRF.value = session.csrf_token
          adoptSession(session)
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

    <aside v-if="sessionExpiring" class="session-warning" role="alert" data-session-expiry-banner>
      <strong>{{ text.sessionExpiringTitle }}</strong>
      <span>{{ text.sessionExpiring }} <time data-session-countdown>{{ sessionCountdown }}</time></span>
      <button type="button" :disabled="busy" @click="extendSession">{{ text.sessionExtend }}</button>
    </aside>

    <header class="app-header">
      <div>
        <p class="eyebrow">ANAS</p>
        <h1>{{ text.product }}</h1>
      </div>
      <button type="button" class="language-button" @click="toggleLocale">{{ text.language }}</button>
    </header>

    <nav v-if="status === 'ready' && sections.length > 1" class="console-nav" :aria-label="text.navLabel">
      <a
        v-for="item in sections"
        :key="item"
        :href="sectionHref(item)"
        :aria-current="section === item ? 'page' : undefined"
      >{{ text[`nav${item.charAt(0).toUpperCase()}${item.slice(1)}` as keyof typeof text] }}</a>
    </nav>

    <main>
      <section v-show="section === 'overview'" class="status-card" aria-live="polite">
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

      <section v-if="status === 'ready' && section === 'overview'" class="auth-card" aria-live="polite">
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
        v-if="canConfigure && system && section === 'config'"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        @saved="configRevision += 1"
      />

      <WorkspaceDeployment
        v-if="canConfigure && system && section === 'deployment'"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :console-state="deploymentConsoleState"
        :authentication-source="system.listener === 'trusted_proxy' ? 'oidc_proxy' : 'local'"
        :config-revision="configRevision"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceLifecycle
        v-if="phase === 'authenticated' && system && section === 'lifecycle'"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceModules
        v-if="phase === 'authenticated' && system && section === 'modules'"
        :workspace-ids="system.workspace_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :refresh-revision="configRevision"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceMaintenance
        v-if="phase === 'authenticated' && system && section === 'maintenance'"
        :workspace-ids="system.workspace_ids"
        :backup-target-ids="system.backup_target_ids"
        :csrf="sessionCSRF"
        :locale="locale"
        :authentication-source="system.listener === 'trusted_proxy' ? 'oidc_proxy' : 'local'"
        @job-created="jobsRevision += 1"
      />

      <WorkspaceJobs
        v-if="canRecoverJobs && section === 'jobs'"
        :locale="locale"
        :refresh-revision="jobsRevision"
      />

      <WorkspaceAudit
        v-if="phase === 'authenticated' && system && section === 'audit'"
        :locale="locale"
        :api-version="system.api_version"
        :cli-version="system.build.version"
      />

      <section v-if="section === 'access'" class="config-card" aria-labelledby="certificate-access-title">
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
