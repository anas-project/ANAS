// TEST_CASES: TEMP-T-021
import test from "node:test";
import assert from "node:assert/strict";
import { focusDocumentCanvas } from "./workspace-temp-storage-collabora-focus.mjs";

class TimeoutError extends Error {}

const WELCOME = ".iframe-welcome-wrap";
const SETTINGS = ".iframe-settings-wrap";
const WELCOME_FRAME = "iframe.iframe-welcome-modal";
const INDICATOR = "#slide-3-indicator";
const CLOSE = "#slide-3-button";
const CANCEL = "#iframe-settings-cancel";

// Model only the public UI boundary. The real helper decides how to dismiss
// known dialogs and whether a blocked ordinary canvas click may be retried.
function frameFixture({ initial = [], thirdPage = false, click, closeFailure,
                        detachFailure, visibleFailure } = {}) {
  const visible = new Set(initial);
  const calls = [];
  let clickCount = 0;
  let thirdPageVisible = thirdPage;
  const queryVisible = async (selector, result) => {
    calls.push({ action: "visible", selector });
    if (visibleFailure?.({ selector, clickCount })) throw visibleFailure.error;
    return result;
  };
  const dialogClick = async (selector, options) => {
    calls.push({ action: "dialog-click", selector, options });
    const wrapper = selector === CANCEL ? SETTINGS : WELCOME;
    assert.equal(visible.has(wrapper), true);
    if (closeFailure?.selector === selector) throw closeFailure.error;
    if (selector === INDICATOR) thirdPageVisible = true;
    if (selector === CLOSE) assert.equal(thirdPageVisible, true);
  };
  const frame = {
    locator(selector) {
      assert.ok(selector === "#document-canvas" || selector === CANCEL ||
                selector === WELCOME || selector === SETTINGS);
      if (selector === "#document-canvas") return {
        async click(options) {
          calls.push({ action: "canvas-click", options });
          clickCount += 1;
          await click?.({ attempt: clickCount, visible });
        },
      };
      if (selector === CANCEL) return {
        click: options => dialogClick(selector, options),
      };
      return {
        isVisible: () => queryVisible(selector, visible.has(selector)),
        async waitFor(options) {
          calls.push({ action: "detach", selector, options });
          if (detachFailure) throw detachFailure;
          visible.delete(selector);
          if (selector === WELCOME) thirdPageVisible = false;
        },
      };
    },
    frameLocator(selector) {
      assert.equal(selector, WELCOME_FRAME);
      return {
        locator(node) {
          assert.ok(node === INDICATOR || node === CLOSE);
          return {
            isVisible: () => queryVisible(node, visible.has(WELCOME) && thirdPageVisible),
            click: options => dialogClick(node, options),
          };
        },
      };
    },
  };
  return { frame, calls, visible, clicks: () => clickCount };
}

function assertNormalActions(calls) {
  for (const call of calls.filter(item => item.action === "canvas-click")) {
    assert.deepEqual(call.options, { position: { x: 250, y: 180 }, timeout: 5_000 });
    assert.equal("force" in call.options, false);
  }
  for (const call of calls.filter(item => item.action === "dialog-click")) {
    assert.ok([INDICATOR, CLOSE, CANCEL].includes(call.selector));
    assert.deepEqual(call.options, { timeout: 30_000 });
    assert.equal("force" in call.options, false);
  }
  for (const call of calls.filter(item => item.action === "detach")) {
    assert.deepEqual(call.options, { state: "detached", timeout: 30_000 });
  }
}

function actions(calls) {
  return calls.filter(call => call.action !== "visible")
    .map(call => call.selector ?? call.action);
}

async function rejectsSame(frame, failure) {
  await assert.rejects(focusDocumentCanvas(frame, TimeoutError), error => error === failure);
}

test("no dialog focuses through one ordinary bounded canvas click", async () => {
  const fixture = frameFixture();
  await focusDocumentCanvas(fixture.frame, TimeoutError);
  assert.equal(fixture.clicks(), 1);
  assert.deepEqual(actions(fixture.calls), ["canvas-click"]);
  assertNormalActions(fixture.calls);
});

test("initial welcome uses its third-page controls and settings uses the parent cancel", async () => {
  const fixture = frameFixture({ initial: [WELCOME, SETTINGS] });
  await focusDocumentCanvas(fixture.frame, TimeoutError);
  assert.deepEqual(actions(fixture.calls), [INDICATOR, CLOSE, WELCOME, CANCEL, SETTINGS, "canvas-click"]);
  assert.equal(fixture.visible.size, 0);
  assert.equal(fixture.clicks(), 1);
  assertNormalActions(fixture.calls);
});

test("welcome already on the third page closes directly without changing pages", async () => {
  const fixture = frameFixture({ initial: [WELCOME], thirdPage: true });
  await focusDocumentCanvas(fixture.frame, TimeoutError);
  assert.deepEqual(actions(fixture.calls), [CLOSE, WELCOME, "canvas-click"]);
  assertNormalActions(fixture.calls);
});

for (const wrapper of [WELCOME, SETTINGS]) {
  test(`late ${wrapper} after a real timeout closes before the second click`, async () => {
    const blocked = new TimeoutError("synthetic blocked canvas");
    const fixture = frameFixture({ click: ({ attempt, visible }) => {
      if (attempt === 1) { visible.add(wrapper); throw blocked; }
      assert.equal(visible.has(wrapper), false);
    } });
    await focusDocumentCanvas(fixture.frame, TimeoutError);
    assert.equal(fixture.clicks(), 2);
    assert.deepEqual(actions(fixture.calls), wrapper === WELCOME
      ? ["canvas-click", INDICATOR, CLOSE, WELCOME, "canvas-click"]
      : ["canvas-click", CANCEL, SETTINGS, "canvas-click"]);
    assertNormalActions(fixture.calls);
  });
}

test("timeouts without a known visible dialog, including an unknown modal, retain the first failure", async () => {
  for (const unknownModal of [null, ".iframe-unknown-wrap"]) {
    const failure = new TimeoutError("synthetic unrelated click timeout");
    const fixture = frameFixture({ click: ({ visible }) => {
      if (unknownModal) visible.add(unknownModal);
      throw failure;
    } });
    await rejectsSame(fixture.frame, failure);
    assert.equal(fixture.clicks(), 1);
    assert.equal(fixture.calls.some(call => call.action === "dialog-click"), false);
  }
});

test("non-timeout and merely named timeout errors cannot trigger a dialog retry", async () => {
  for (const failure of [new Error("synthetic unknown click failure"),
                         Object.assign(new Error("synthetic foreign timeout"), { name: "TimeoutError" })]) {
    const fixture = frameFixture({ click: ({ visible }) => { visible.add(WELCOME); throw failure; } });
    await rejectsSame(fixture.frame, failure);
    assert.equal(fixture.clicks(), 1);
    assert.equal(fixture.calls.some(call => call.action === "dialog-click"), false);
  }
});

test("late known control or detach failures cannot replace the original click timeout", async () => {
  for (const selector of [INDICATOR, CLOSE, CANCEL, null]) {
    const primary = new TimeoutError("synthetic primary click timeout");
    const secondary = new Error("synthetic dialog closure failure");
    const wrapper = selector === CANCEL ? SETTINGS : WELCOME;
    const fixture = frameFixture({
      ...(selector ? { closeFailure: { selector, error: secondary } } : { detachFailure: secondary }),
      click: ({ visible }) => { visible.add(wrapper); throw primary; },
    });
    await rejectsSame(fixture.frame, primary);
    assert.equal(fixture.clicks(), 1);
    assertNormalActions(fixture.calls);
  }
});

test("late wrapper or welcome button visibility failures retain the original timeout", async () => {
  for (const selector of [WELCOME, CLOSE]) {
    const primary = new TimeoutError("synthetic primary click timeout");
    const visibility = Object.assign(({ selector: current, clickCount }) => clickCount > 0 && current === selector,
      { error: new Error("synthetic visibility failure") });
    const fixture = frameFixture({ visibleFailure: visibility,
      click: ({ visible }) => { visible.add(WELCOME); throw primary; } });
    await rejectsSame(fixture.frame, primary);
    assert.equal(fixture.clicks(), 1);
    assert.equal(fixture.calls.some(call => call.action === "dialog-click"), false);
  }
});

test("initial known control or detach failures are fatal before any canvas click", async () => {
  for (const selector of [INDICATOR, CLOSE, CANCEL, null]) {
    const failure = new Error("synthetic initial dialog failure");
    const wrapper = selector === CANCEL ? SETTINGS : WELCOME;
    const fixture = frameFixture({ initial: [wrapper],
      ...(selector ? { closeFailure: { selector, error: failure } } : { detachFailure: failure }),
    });
    await rejectsSame(fixture.frame, failure);
    assert.equal(fixture.clicks(), 0);
    assertNormalActions(fixture.calls);
  }
});

test("reappearing known dialogs stop after three bounded clicks without force", async () => {
  const failures = Array.from({ length: 3 }, (_, n) => new TimeoutError(`synthetic blocked click ${n}`));
  const fixture = frameFixture({ click: ({ attempt, visible }) => {
    assert.ok(attempt <= 3);
    visible.add(WELCOME);
    throw failures[attempt - 1];
  } });
  await rejectsSame(fixture.frame, failures[2]);
  assert.equal(fixture.clicks(), 3);
  assert.equal(fixture.calls.filter(call => call.action === "dialog-click" && call.selector === CLOSE).length, 2);
  assert.equal(fixture.calls.filter(call => call.action === "detach").length, 2);
  assertNormalActions(fixture.calls);
});
