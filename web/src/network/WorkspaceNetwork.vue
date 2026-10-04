<script setup lang="ts">
// REQUIREMENTS: INCUS-R-128 INCUS-R-129
import { computed, onMounted, ref, watch } from "vue"

import { getWorkspaceLeaseNetworks, type LeaseNetwork, type LeaseNetworkResponse } from "../api/network"
import { APIProblemError, problemMessage } from "../api/problems"
import { messages, type Locale, type MessageKey } from "../i18n/messages"
import { leaseDiagram } from "./model"

const props = defineProps<{
  workspaceIds: string[]
  locale: Locale
}>()

const selectedWorkspace = ref(props.workspaceIds[0] ?? "")
const snapshot = ref<LeaseNetworkResponse | null>(null)
const loading = ref(false)
const errorCode = ref<string | null>(null)
const text = computed(() => messages[props.locale])

function peerLabel(peer: string): string {
  const key = `networkPeer_${peer}` as MessageKey
  return key in text.value ? text.value[key] : peer
}

function onOff(value: boolean): string {
  return value ? text.value.networkOn : text.value.networkOff
}

async function load(): Promise<void> {
  if (selectedWorkspace.value === "" || loading.value) return
  loading.value = true
  errorCode.value = null
  try {
    snapshot.value = await getWorkspaceLeaseNetworks(selectedWorkspace.value)
  } catch (error) {
    snapshot.value = null
    errorCode.value = error instanceof APIProblemError ? error.code : "request_failed"
  } finally {
    loading.value = false
  }
}

function leaseKey(lease: LeaseNetwork): string {
  return `${lease.consumer}.${lease.resource}`
}

watch(selectedWorkspace, () => void load())
onMounted(() => void load())
</script>

<template>
  <section class="config-card" aria-labelledby="network-title" aria-live="polite">
    <div class="config-heading">
      <div>
        <p class="eyebrow">{{ text.networkEyebrow }}</p>
        <h2 id="network-title">{{ text.networkTitle }}</h2>
        <p>{{ text.networkHelp }}</p>
      </div>
      <div class="network-controls">
        <label v-if="props.workspaceIds.length > 1">
          {{ text.networkWorkspace }}
          <select v-model="selectedWorkspace">
            <option v-for="id in props.workspaceIds" :key="id" :value="id">{{ id }}</option>
          </select>
        </label>
        <button type="button" class="secondary-button" :disabled="loading" @click="load">{{ text.networkReload }}</button>
      </div>
    </div>

    <p v-if="errorCode" class="error-message" role="alert">
      <strong>{{ text.errorTitle }}</strong> {{ problemMessage(props.locale, errorCode) }}
    </p>
    <p v-if="loading" class="muted">{{ text.networkLoading }}</p>
    <p v-else-if="snapshot && snapshot.active_deployment === null" class="muted">{{ text.networkNoDeployment }}</p>
    <p v-else-if="snapshot && snapshot.leases.length === 0" class="muted">{{ text.networkEmpty }}</p>

    <!-- Every value is interpolated text; nothing from the server is rendered as markup. -->
    <article v-for="lease in snapshot?.leases ?? []" :key="leaseKey(lease)" class="network-lease" :data-lease="leaseKey(lease)">
      <h3>{{ text.networkLease }} <code>{{ leaseKey(lease) }}</code> <span class="muted">{{ lease.interface }}</span></h3>
      <dl class="network-facts">
        <div><dt>{{ text.networkBridge }}</dt><dd><code>{{ lease.bridge || "—" }}</code></dd></div>
        <div><dt>{{ text.networkSubnet }}</dt><dd><code>{{ lease.ipv4_subnet || "—" }}</code><template v-if="lease.ipv6_subnet"> · <code>{{ lease.ipv6_subnet }}</code></template></dd></div>
        <div><dt>{{ text.networkGateway }}</dt><dd><code>{{ lease.ipv4_gateway || "—" }}</code></dd></div>
        <div><dt>{{ text.networkEgress }}</dt><dd><code>{{ lease.egress }}</code></dd></div>
        <div><dt>{{ text.networkModuleAccess }}</dt><dd>{{ onOff(lease.module_access) }}</dd></div>
        <div><dt>{{ text.networkIntraLease }}</dt><dd>{{ onOff(lease.intra_lease) }}</dd></div>
        <div><dt>{{ text.networkIngress }}</dt><dd><code>{{ lease.ingress }}</code></dd></div>
        <div><dt>{{ text.networkStatus }}</dt><dd>{{ lease.status }}</dd></div>
      </dl>

      <div class="network-diagram-scroll">
        <svg
          v-for="diagram in [leaseDiagram(lease)]"
          :key="'diagram'"
          class="network-diagram"
          role="img"
          :aria-label="`${text.networkLease} ${leaseKey(lease)}`"
          :viewBox="`0 0 ${diagram.width} ${diagram.height + 24}`"
          :width="diagram.width"
          :height="diagram.height + 24"
        >
          <defs>
            <marker id="arrow-allowed" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" class="network-arrow allowed" />
            </marker>
            <marker id="arrow-denied" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
              <path d="M0,0 L10,5 L0,10 z" class="network-arrow denied" />
            </marker>
          </defs>
          <text :x="diagram.columns.outbound" y="12" class="network-column-title">{{ text.networkOutbound }}</text>
          <text :x="diagram.columns.inbound" y="12" class="network-column-title">{{ text.networkInbound }}</text>
          <g :transform="'translate(0, 20)'">
            <rect :x="diagram.columns.lease" :y="diagram.lease.y" :width="diagram.columns.boxWidth" :height="diagram.lease.height" rx="8" class="network-lease-box" />
            <text :x="diagram.columns.lease + 12" :y="diagram.lease.y + 22" class="network-lease-title">{{ lease.bridge || lease.sandbox }}</text>
            <text :x="diagram.columns.lease + 12" :y="diagram.lease.y + 42" class="network-lease-detail">{{ lease.ipv4_subnet }}</text>
            <text
              v-for="(instance, index) in lease.instances.slice(0, Math.max(0, Math.floor((diagram.lease.height - 60) / 18)))"
              :key="instance.name"
              :x="diagram.columns.lease + 12"
              :y="diagram.lease.y + 66 + index * 18"
              class="network-lease-instance"
            >{{ instance.name }} {{ instance.addresses[0] ?? "" }}</text>
            <g v-for="row in diagram.outbound" :key="'out-' + row.peer">
              <rect :x="diagram.columns.outbound" :y="row.y - 13" :width="diagram.columns.boxWidth" height="26" rx="5" :class="['network-peer', row.allowed ? 'allowed' : 'denied']" />
              <text :x="diagram.columns.outbound + 8" :y="row.y + 4" class="network-peer-label">{{ peerLabel(row.peer) }}</text>
              <line
                :x1="diagram.columns.lease"
                :y1="row.y"
                :x2="diagram.columns.outbound + diagram.columns.boxWidth + 4"
                :y2="row.y"
                :class="['network-edge', row.allowed ? 'allowed' : 'denied']"
                :marker-end="row.allowed ? 'url(#arrow-allowed)' : 'url(#arrow-denied)'"
              ><title>{{ peerLabel(row.peer) }}: {{ row.allowed ? text.networkAllowed : text.networkDenied }}</title></line>
            </g>
            <g v-for="row in diagram.inbound" :key="'in-' + row.peer">
              <rect :x="diagram.columns.inbound" :y="row.y - 13" :width="diagram.columns.boxWidth" height="26" rx="5" :class="['network-peer', row.allowed ? 'allowed' : 'denied']" />
              <text :x="diagram.columns.inbound + 8" :y="row.y + 4" class="network-peer-label">{{ peerLabel(row.peer) }}</text>
              <line
                :x1="diagram.columns.inbound"
                :y1="row.y"
                :x2="diagram.columns.lease + diagram.columns.boxWidth + 4"
                :y2="row.y"
                :class="['network-edge', row.allowed ? 'allowed' : 'denied']"
                :marker-end="row.allowed ? 'url(#arrow-allowed)' : 'url(#arrow-denied)'"
              ><title>{{ peerLabel(row.peer) }}: {{ row.allowed ? text.networkAllowed : text.networkDenied }}</title></line>
            </g>
          </g>
        </svg>
      </div>

      <h4>{{ text.networkInstances }}</h4>
      <p v-if="lease.instances_error" class="error-message" role="status">{{ text.networkInstancesError }}</p>
      <p v-else-if="lease.instances.length === 0" class="muted">{{ text.networkNoInstances }}</p>
      <table v-else class="audit-table">
        <thead><tr><th scope="col">{{ text.networkInstances }}</th><th scope="col">{{ text.networkStatus }}</th><th scope="col">{{ text.networkAddress }}</th><th scope="col">{{ text.networkSlot }}</th></tr></thead>
        <tbody>
          <tr v-for="instance in lease.instances" :key="instance.name">
            <td><code>{{ instance.name }}</code></td>
            <td>{{ instance.status }}</td>
            <td>{{ instance.addresses.join(", ") || "—" }}</td>
            <td>{{ instance.slot || "—" }}</td>
          </tr>
        </tbody>
      </table>

      <h4>{{ text.networkHTTP }}</h4>
      <p class="muted">{{ text.networkHTTPPorts }}: {{ lease.http_ports.join(", ") || "—" }}</p>
      <p v-if="lease.http_publications.length === 0" class="muted">{{ text.networkNoHTTP }}</p>
      <table v-else class="audit-table">
        <thead><tr><th scope="col">{{ text.networkHost }}</th><th scope="col">{{ text.networkInstances }}</th><th scope="col">{{ text.networkAddress }}</th><th scope="col">{{ text.networkGuestPort }}</th></tr></thead>
        <tbody>
          <tr v-for="publication in lease.http_publications" :key="publication.host">
            <td><code>{{ publication.host }}</code></td>
            <td><code>{{ publication.instance }}</code></td>
            <td>{{ publication.address }}</td>
            <td>{{ publication.port }}</td>
          </tr>
        </tbody>
      </table>

      <h4>{{ text.networkPorts }}</h4>
      <p v-if="lease.port_bindings.length === 0" class="muted">{{ text.networkNoPorts }}</p>
      <table v-else class="audit-table">
        <thead><tr><th scope="col">{{ text.networkHostPort }}</th><th scope="col">{{ text.networkSlot }}</th><th scope="col">{{ text.networkAddress }}</th><th scope="col">{{ text.networkGuestPort }}</th></tr></thead>
        <tbody>
          <tr v-for="binding in lease.port_bindings" :key="binding.protocol + binding.host_port">
            <td><code>{{ binding.protocol }}/{{ binding.host_port }}</code><span v-if="binding.auto" class="muted"> (auto)</span></td>
            <td>{{ binding.slot }} <code>{{ binding.instance }}</code></td>
            <td>{{ [binding.ipv4, binding.ipv6].filter(Boolean).join(", ") || "—" }}</td>
            <td>{{ binding.guest_port }}</td>
          </tr>
        </tbody>
      </table>
    </article>
  </section>
</template>
