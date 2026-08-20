#!/usr/bin/env node

"use strict";

const requiredMetadataFields = [
  "issuer",
  "authorization_endpoint",
  "token_endpoint",
  "jwks_uri",
];

function normalizeIssuer(value) {
  const url = new URL(value);
  url.hash = "";
  url.search = "";
  return url.href.replace(/\/$/, "");
}

async function probeOIDC(
  discoveryURL,
  expectedIssuer,
  requestTimeoutMs,
  fetchImpl = fetch,
) {
  const response = await fetchImpl(discoveryURL, {
    headers: { accept: "application/json" },
    redirect: "error",
    signal: AbortSignal.timeout(requestTimeoutMs),
  });
  if (!response.ok) {
    throw new Error(`expected 200 OK, got ${response.status} ${response.statusText}`);
  }

  const metadata = await response.json();
  for (const field of requiredMetadataFields) {
    if (typeof metadata[field] !== "string" || metadata[field] === "") {
      throw new Error(`discovery metadata has no ${field}`);
    }
  }
  if (normalizeIssuer(metadata.issuer) !== normalizeIssuer(expectedIssuer)) {
    throw new Error(
      `discovery issuer ${JSON.stringify(metadata.issuer)} does not match ` +
        JSON.stringify(expectedIssuer),
    );
  }
  return metadata;
}

async function waitForOIDC({
  discoveryURL,
  expectedIssuer,
  maxAttempts = 300,
  retryMs = 2000,
  requestTimeoutMs = 5000,
  logger = console,
  fetchImpl = fetch,
}) {
  if (!Number.isInteger(maxAttempts) || maxAttempts < 1) {
    throw new Error("maxAttempts must be a positive integer");
  }

  let lastError;
  for (let attempt = 1; attempt <= maxAttempts; attempt += 1) {
    try {
      const metadata = await probeOIDC(
        discoveryURL,
        expectedIssuer,
        requestTimeoutMs,
        fetchImpl,
      );
      logger.log(`OIDC discovery is ready: ${metadata.issuer}`);
      return metadata;
    } catch (error) {
      lastError = error;
      if (attempt === maxAttempts) break;
      if (attempt === 1 || attempt % 15 === 0) {
        logger.warn(
          `OIDC discovery is not ready (attempt ${attempt}/${maxAttempts}): ` +
            error.message,
        );
      }
      await new Promise((resolve) => setTimeout(resolve, retryMs));
    }
  }

  throw new Error(
    `OIDC discovery did not become ready after ${maxAttempts} attempts: ` +
      lastError.message,
  );
}

async function main() {
  const discoveryURL = process.env.MESHCENTRAL_OIDC_DISCOVERY_URL;
  const expectedIssuer = process.env.MESHCENTRAL_OIDC_ISSUER_URL;
  if (!discoveryURL || !expectedIssuer) {
    throw new Error(
      "MESHCENTRAL_OIDC_DISCOVERY_URL and MESHCENTRAL_OIDC_ISSUER_URL are required",
    );
  }
  await waitForOIDC({ discoveryURL, expectedIssuer });
}

if (require.main === module) {
  main().catch((error) => {
    console.error(error.message);
    process.exit(1);
  });
}

module.exports = { normalizeIssuer, probeOIDC, waitForOIDC };
