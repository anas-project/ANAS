// REQUIREMENTS: CONSOLE-R-127 CONSOLE-R-133
export interface CredentialExposureEnvironment {
  windowTarget: Pick<EventTarget, "addEventListener" | "removeEventListener">
  documentTarget: Pick<EventTarget, "addEventListener" | "removeEventListener">
  setTimer(callback: () => void, milliseconds: number): ReturnType<typeof setTimeout>
  clearTimer(timer: ReturnType<typeof setTimeout>): void
}

export function beginCredentialExposure(
  clear: () => void,
  milliseconds = 30_000,
  environment: CredentialExposureEnvironment = {
    windowTarget: window,
    documentTarget: document,
    setTimer: (callback, delay) => window.setTimeout(callback, delay),
    clearTimer: (timer) => window.clearTimeout(timer),
  },
): () => void {
  let active = true
  let timer: ReturnType<typeof setTimeout>
  const stop = (): void => {
    if (!active) return
    active = false
    environment.windowTarget.removeEventListener("blur", expire)
    environment.documentTarget.removeEventListener("visibilitychange", expire)
    environment.clearTimer(timer)
  }
  const expire = (): void => {
    if (!active) return
    clear()
    stop()
  }
  environment.windowTarget.addEventListener("blur", expire)
  environment.documentTarget.addEventListener("visibilitychange", expire)
  timer = environment.setTimer(expire, milliseconds)
  return stop
}
