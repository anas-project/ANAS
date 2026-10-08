<script setup lang="ts">
// REQUIREMENTS: TEMP-R-038
import { computed } from "vue"
import type { components } from "../api/schema"
import { messages, type Locale } from "../i18n/messages"

const props = defineProps<{ plan: components["schemas"]["TempSwitchPlan"] | undefined; locale: Locale }>()
const text = computed(() => messages[props.locale])
</script>

<template>
  <aside v-if="plan?.required" class="risk-confirmation" role="alert" data-temp-switch>
    <strong>{{ text.tempSwitchTitle }}</strong>
    <p v-if="plan.session_interruption">{{ text.tempSwitchSessions }}</p>
    <div class="plan-grid">
      <article>
        <h4>{{ text.tempSwitchStop }}</h4>
        <ol><li v-for="module in plan.stop_modules" :key="module">{{ module }}</li></ol>
      </article>
      <article>
        <h4>{{ text.tempSwitchStart }}</h4>
        <ol><li v-for="module in plan.start_modules" :key="module">{{ module }}</li></ol>
      </article>
    </div>
  </aside>
</template>
