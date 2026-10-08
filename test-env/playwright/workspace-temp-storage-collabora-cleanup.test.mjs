// TEST_CASES: TEMP-T-023
import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { cleanupDocument } from "./workspace-temp-storage-collabora-cleanup.mjs";

async function fixture(run) {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "anas-collabora-cleanup-"));
  try { await run(path.join(directory, "cleanup.json")); }
  finally { fs.rmSync(directory, { recursive: true, force: true }); }
}

test("delete and dispose failures preserve the first editor failure and report fixed codes", async () => {
  await fixture(async (reportFile) => {
    const firstFailure = new Error("synthetic first editor failure");
    let disposed = false;
    const result = await cleanupDocument({ created: true, firstFailure, reportFile, runId: "local-only",
      remove: async () => { throw new Error("synthetic-password"); },
      dispose: async () => { disposed = true; throw new Error("synthetic-password"); } });
    assert.equal(result.failure, firstFailure);
    assert.equal(disposed, true);
    assert.equal(result.report.status, "failed");
    assert.equal(result.report.document, "unconfirmed");
    assert.equal(result.report.first_failure_preserved, true);
    assert.deepEqual(result.report.failures, ["temporary_document_delete_failed", "webdav_context_dispose_failed"]);
    assert.equal(fs.statSync(reportFile).mode & 0o777, 0o600);
    assert.equal(fs.readFileSync(reportFile, "utf8").includes("synthetic-password"), false);
  });
});

test("cleanup alone fails the test for an unsuccessful HTTP deletion", async () => {
  await fixture(async (reportFile) => {
    const result = await cleanupDocument({ created: true, reportFile, runId: "local-only",
      remove: async () => ({ status: () => 500 }), dispose: async () => {} });
    assert.match(result.failure.message, /temporary document cleanup failed/);
    assert.equal(result.report.first_failure_preserved, false);
    assert.equal(result.report.status, "failed");
  });
});

test("actual 204 or 404 deletion plus context closure permits complete cleanup", async () => {
  await fixture(async (reportFile) => {
    for (const status of [204, 404]) {
      const result = await cleanupDocument({ created: true, reportFile, runId: "local-only",
        remove: async () => ({ status: () => status }), dispose: async () => {} });
      assert.equal(result.failure, undefined);
      assert.equal(result.report.status, "complete");
      assert.equal(result.report.document, "removed");
      assert.equal(result.report.webdav_context, "closed");
    }
  });
});

test("report write failure preserves an earlier failure and blocks a passing test", async () => {
  await fixture(async (reportFile) => {
    const firstFailure = new Error("synthetic first lifecycle failure");
    const failedPath = path.join(reportFile, "missing-parent.json");
    const options = { created: false, reportFile: failedPath, runId: "local-only", remove: async () => {}, dispose: async () => {} };
    assert.equal((await cleanupDocument({ ...options, firstFailure })).failure, firstFailure);
    assert.match((await cleanupDocument(options)).failure.message, /cleanup report write failed/);
  });
});
