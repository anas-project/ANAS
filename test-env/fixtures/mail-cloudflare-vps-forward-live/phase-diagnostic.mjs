// TEST_CASES: MAILMOD-T-009
// Select only fixed test messages: the remaining output can contain private data.
const verificationFailures = [
  'isolated verification inbox unconfirmed',
  'verification message bound to unexpected claim',
  'fresh private message path required',
  'verification message download unconfirmed',
  'verification message unavailable',
];
const preflightFailures = [
  'destination-preflight-unavailable',
  'destination-preflight-authentication',
  'destination-preflight-rate-limited',
  'destination-preflight-conflict',
  'destination-preflight-identity',
  'destination-preflight-unknown',
];

export function phaseFailure(name, stdout, stderr) {
  if (name === 'read-verification') {
    const output = `${stdout || ''}\n${stderr || ''}`;
    const matches = verificationFailures.filter(message => output.includes(message));
    if (matches.length === 1) return `isolated route client ${name} failed: ${matches[0]}`;
  }
  if (name === 'destination-preflight') {
    const output = `${stdout || ''}\n${stderr || ''}`;
    const matches = preflightFailures.filter(message => output.includes(message));
    if (matches.length === 1) return `isolated route client ${name} failed: ${matches[0]}`;
  }
  return `isolated route client ${name} failed`;
}

// The operation is a provider GET only. Never retry authentication, conflict,
// malformed results, rate limiting, or a mutation with an unknown outcome.
export async function retryDestinationPreflight(read, wait) {
  for (let attempt=0; attempt<3; attempt++) {
    try { return await read(); }
    catch (error) {
      if (attempt===2 || error?.message !== 'isolated route client destination-preflight failed: destination-preflight-unavailable') throw error;
      await wait(2000);
    }
  }
}

// The SMTP probe stops before DATA. Preserve only its constrained numeric
// recipient rejections, never arbitrary remote text or message contents.
export function recipientProbeFailure(stderr) {
  const lines=String(stderr || '').split(/\r?\n/);
  const matches=lines.filter(line=>/^smtp-recipient-rejected:[45][0-9]{2}(?:\/[45]\.[0-9]{1,3}\.[0-9]{1,3})?(?:,[45][0-9]{2}(?:\/[45]\.[0-9]{1,3}\.[0-9]{1,3})?){0,5}$/.test(line));
  return matches.length===1 ? `recipient-preflight-${matches[0]}` : 'recipient-preflight-unconfirmed';
}
