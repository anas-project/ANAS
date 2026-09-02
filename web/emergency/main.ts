// REQUIREMENTS: CONSOLE-R-113
import "./styles.css"

const button = document.querySelector<HTMLButtonElement>("#health-check")
const result = document.querySelector<HTMLOutputElement>("#health-result")

button?.addEventListener("click", async () => {
  if (!result) return
  result.textContent = "…"
  try {
    const response = await fetch("/healthz", { credentials: "omit", cache: "no-store" })
    const body = (await response.json()) as { status?: unknown }
    result.textContent = response.ok && body.status === "ok" ? "OK" : `HTTP ${response.status}`
  } catch {
    result.textContent = "Unavailable / 不可用"
  }
})
