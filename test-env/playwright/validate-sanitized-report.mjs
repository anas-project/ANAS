import fs from "node:fs";
import { pathToFileURL } from "node:url";

const secretPatterns = [
  /([?&](?:code|token|id_token|access_token|refresh_token|SAMLRequest|SAMLResponse|RelayState|Signature)=)(?!\[redacted\])[^&\s"]+/i,
  /Bearer\s+(?!\[redacted\])[^\s"]+/i,
  /eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/,
];

export function validateReport(reportFile, expected = process.env) {
  const stat = fs.statSync(reportFile);
  if ((stat.mode & 0o777) !== 0o600) {
    throw new Error(`IAM logout report must have mode 0600, got ${(stat.mode & 0o777).toString(8)}`);
  }

  const raw = fs.readFileSync(reportFile, "utf8");
  const report = JSON.parse(raw);
  if (report.schema !== "anas.iam-logout-e2e/v1") throw new Error("unexpected IAM logout report schema");
  if (report.status !== "passed") throw new Error(`IAM logout report status is ${report.status || "missing"}`);

  const fixture = report.fixture || {};
  const wanted = {
    provider: expected.ANAS_TEST_IAM_PROVIDER,
    protocol: expected.ANAS_TEST_IAM_PROTOCOL,
    application: expected.ANAS_TEST_APP,
    origin: expected.ANAS_TEST_LOGOUT_ORIGIN,
    provider_version: expected.ANAS_TEST_PROVIDER_VERSION,
    application_version: expected.ANAS_TEST_APP_VERSION,
  };
  for (const [field, value] of Object.entries(wanted)) {
    if (!value || fixture[field] !== value) {
      throw new Error(`IAM logout report fixture ${field} mismatch`);
    }
  }
  if (!Array.isArray(report.results) || report.results.length === 0) {
    throw new Error("IAM logout report contains no executed tests");
  }
  if (report.results.some((result) => result.status !== "passed")) {
    throw new Error("IAM logout report contains a non-passing test");
  }

  for (const secret of [expected.ANAS_TEST_PASSWORD, expected.ANAS_TEST_USERNAME]) {
    if (secret && raw.includes(secret)) throw new Error("IAM logout report contains a fixture credential");
  }
  if (secretPatterns.some((pattern) => pattern.test(raw))) {
    throw new Error("IAM logout report contains an unredacted protocol credential or message");
  }
  return report;
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const reportFile = process.argv[2];
  if (!reportFile) throw new Error("usage: validate-sanitized-report.mjs <report-file>");
  const report = validateReport(reportFile);
  process.stdout.write(`validated IAM logout report: ${report.results.length} passed test(s)\n`);
}
