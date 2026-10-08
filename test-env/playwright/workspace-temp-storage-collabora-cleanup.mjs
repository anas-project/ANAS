// TEST_CASES: TEMP-T-021, TEMP-T-023
import fs from "node:fs";

// Cleanup records fixed result codes separately; its failures never replace an
// earlier editor or lifecycle failure with a less useful secondary exception.
export async function cleanupDocument({ created, remove, dispose, firstFailure, reportFile, runId }) {
  let failure = firstFailure;
  const failures = [];
  let document = created ? "unconfirmed" : "not_created";
  if (created) {
    try {
      const response = await remove();
      if (!response || ![204, 404].includes(response.status())) throw new Error("delete failed");
      document = "removed";
    } catch {
      failures.push("temporary_document_delete_failed");
      failure ||= new Error("temporary document cleanup failed");
    }
  }
  let webdavContext = "closed";
  try { await dispose(); }
  catch {
    webdavContext = "failed";
    failures.push("webdav_context_dispose_failed");
    failure ||= new Error("WebDAV context cleanup failed");
  }
  const report = {
    schema: "anas.workspace-temp-collabora-cleanup/v1", case_id: "TEMP-T-021", run_id: runId,
    status: failures.length ? "failed" : "complete", document, webdav_context: webdavContext,
    first_failure_preserved: Boolean(firstFailure), failures,
  };
  try {
    fs.writeFileSync(reportFile, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
    fs.chmodSync(reportFile, 0o600);
  } catch {
    failure ||= new Error("browser cleanup report write failed");
  }
  return { failure, report };
}
