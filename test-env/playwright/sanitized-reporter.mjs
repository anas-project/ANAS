import fs from "node:fs";
import path from "node:path";

function redact(raw) {
  let value = String(raw || "");
  for (const secret of [process.env.ANAS_TEST_PASSWORD, process.env.ANAS_TEST_USERNAME]) {
    if (secret) value = value.split(secret).join("[redacted]");
  }
  return value
    .replace(/([?&](?:code|token|id_token|access_token|refresh_token|SAMLRequest|SAMLResponse|RelayState|Signature)=)[^&\s]+/gi, "$1[redacted]")
    .replace(/Bearer\s+[^\s]+/gi, "Bearer [redacted]")
    .replace(/eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/g, "[jwt-redacted]");
}

export default class SanitizedReporter {
  constructor(options = {}) {
    this.outputFile = options.outputFile || path.resolve("test-env/reports/iam-logout-playwright.json");
    this.results = [];
    this.startedAt = new Date().toISOString();
  }

  onTestEnd(test, result) {
    this.results.push({
      title: test.title,
      status: result.status,
      duration_ms: result.duration,
      errors: result.errors.map((error) => redact(error.message)),
    });
  }

  onEnd(result) {
    fs.mkdirSync(path.dirname(this.outputFile), { recursive: true });
    const report = {
      schema: "anas.iam-logout-e2e/v1",
      started_at: this.startedAt,
      completed_at: new Date().toISOString(),
      status: result.status,
      fixture: {
        provider: process.env.ANAS_TEST_IAM_PROVIDER || "unset",
        protocol: process.env.ANAS_TEST_IAM_PROTOCOL || "unset",
        application: process.env.ANAS_TEST_APP || "unset",
        provider_version: process.env.ANAS_TEST_PROVIDER_VERSION || "fixture-managed",
        application_version: process.env.ANAS_TEST_APP_VERSION || "fixture-managed",
      },
      results: this.results,
    };
    fs.writeFileSync(this.outputFile, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
    fs.chmodSync(this.outputFile, 0o600);
  }
}
