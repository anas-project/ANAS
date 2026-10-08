// TEST_CASES: TEMP-T-010
import type { Environment } from "vitest/environments"

// Compile Vue client render functions for the in-memory host renderer. This
// environment requires neither a DOM emulator nor a browser process.
export default {
  name: "vue-host",
  viteEnvironment: "client",
  setup: () => ({ teardown() {} }),
} satisfies Environment
