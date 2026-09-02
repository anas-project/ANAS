import { describe, expect, it, vi } from "vitest"

import { beginCredentialExposure } from "./model"

function environment() {
  const windowTarget = new EventTarget()
  const documentTarget = new EventTarget()
  let timer: (() => void) | null = null
  return {
    windowTarget,
    documentTarget,
    runTimer: () => timer?.(),
    value: {
      windowTarget,
      documentTarget,
      setTimer(callback: () => void) {
        timer = callback
        return 1 as unknown as ReturnType<typeof setTimeout>
      },
      clearTimer() {
        timer = null
      },
    },
  }
}

describe("credential exposure", () => {
  it("clears on page blur", () => {
    const fixture = environment()
    const clear = vi.fn()
    beginCredentialExposure(clear, 30_000, fixture.value)
    fixture.windowTarget.dispatchEvent(new Event("blur"))
    expect(clear).toHaveBeenCalledOnce()
  })

  it("clears on visibility change", () => {
    const fixture = environment()
    const clear = vi.fn()
    beginCredentialExposure(clear, 30_000, fixture.value)
    fixture.documentTarget.dispatchEvent(new Event("visibilitychange"))
    expect(clear).toHaveBeenCalledOnce()
  })

  it("clears after the timeout and disposal cancels clearing", () => {
    const timed = environment()
    const clear = vi.fn()
    beginCredentialExposure(clear, 30_000, timed.value)
    timed.runTimer()
    expect(clear).toHaveBeenCalledOnce()

    const disposed = environment()
    const clearDisposed = vi.fn()
    const stop = beginCredentialExposure(clearDisposed, 30_000, disposed.value)
    stop()
    disposed.runTimer()
    disposed.windowTarget.dispatchEvent(new Event("blur"))
    expect(clearDisposed).not.toHaveBeenCalled()
  })
})
