import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import SanitizedReporter from "./sanitized-reporter.mjs";
import { validateReport } from "./validate-sanitized-report.mjs";

function fixtureEnvironment() {
  return {
    ANAS_TEST_IAM_PROVIDER: "authentik",
    ANAS_TEST_IAM_PROTOCOL: "oidc",
    ANAS_TEST_APP: "nextcloud",
    ANAS_TEST_LOGOUT_ORIGIN: "module",
    ANAS_TEST_PROVIDER_VERSION: "2026.5.6",
    ANAS_TEST_APP_VERSION: "34.0.2 / user_oidc 8.10.1",
    ANAS_TEST_USERNAME: "logout-e2e-user",
    ANAS_TEST_PASSWORD: "logout-e2e-password",
  };
}

test("reporter writes a non-empty redacted 0600 report accepted by the validator", () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "anas-iam-logout-report-"));
  const reportFile = path.join(directory, "report.json");
  const previous = {};
  const environment = fixtureEnvironment();
  for (const [key, value] of Object.entries(environment)) {
    previous[key] = process.env[key];
    process.env[key] = value;
  }

  try {
    const reporter = new SanitizedReporter({ outputFile: reportFile });
    reporter.onTestEnd(
      { title: `nextcloud ${environment.ANAS_TEST_USERNAME}` },
      {
        status: "passed",
        duration: 42,
        errors: [{ message: `Bearer secret-token ${environment.ANAS_TEST_PASSWORD} ?code=secret-code eyJabc.def.ghi` }],
      },
    );
    reporter.onEnd({ status: "passed" });

    const report = validateReport(reportFile, environment);
    assert.equal(report.results.length, 1);
    const raw = fs.readFileSync(reportFile, "utf8");
    assert.equal(raw.includes(environment.ANAS_TEST_USERNAME), false);
    assert.equal(raw.includes(environment.ANAS_TEST_PASSWORD), false);
    assert.match(raw, /\[redacted\]/);
  } finally {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    fs.rmSync(directory, { recursive: true, force: true });
  }
});

test("validator rejects an empty successful report", () => {
  const directory = fs.mkdtempSync(path.join(os.tmpdir(), "anas-iam-logout-empty-"));
  const reportFile = path.join(directory, "report.json");
  fs.writeFileSync(reportFile, JSON.stringify({
    schema: "anas.iam-logout-e2e/v1",
    status: "passed",
    fixture: {
      provider: "authentik",
      protocol: "oidc",
      application: "nextcloud",
      origin: "module",
      provider_version: "2026.5.6",
      application_version: "34.0.2 / user_oidc 8.10.1",
    },
    results: [],
  }), { mode: 0o600 });
  try {
    assert.throws(() => validateReport(reportFile, fixtureEnvironment()), /no executed tests/);
  } finally {
    fs.rmSync(directory, { recursive: true, force: true });
  }
});
