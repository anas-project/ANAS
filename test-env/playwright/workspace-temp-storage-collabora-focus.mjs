// TEST_CASES: TEMP-T-021
async function closeKnownDialogs(frame) {
  let closed = false;
  for (const wrapper of [".iframe-welcome-wrap", ".iframe-settings-wrap"]) {
    const dialog = frame.locator(wrapper);
    if (!await dialog.isVisible()) continue;
    if (wrapper === ".iframe-welcome-wrap") {
      // The fixed CODE image closes through its visible welcome controls.
      const welcome = frame.frameLocator("iframe.iframe-welcome-modal");
      const close = welcome.locator("#slide-3-button");
      if (!await close.isVisible()) {
        await welcome.locator("#slide-3-indicator").click({ timeout: 30_000 });
      }
      await close.click({ timeout: 30_000 });
    } else {
      await frame.locator("#iframe-settings-cancel").click({ timeout: 30_000 });
    }
    await dialog.waitFor({ state: "detached", timeout: 30_000 });
    closed = true;
  }
  return closed;
}

export async function focusDocumentCanvas(frame, TimeoutError) {
  await closeKnownDialogs(frame);
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      await frame.locator("#document-canvas").click({ position: { x: 250, y: 180 }, timeout: 5_000 });
      return;
    } catch (error) {
      // A known dialog can appear after the initial visibility check. Retry
      // only after normal dismissal; unknown failures retain the first error.
      if (!(error instanceof TimeoutError) || attempt === 2) throw error;
      try {
        if (await closeKnownDialogs(frame)) continue;
      } catch { /* A dismissal failure must not replace the blocked click. */ }
      throw error;
    }
  }
}
