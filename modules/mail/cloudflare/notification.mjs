// Notification authentication/transport only. R2 and delivery receipts remain
// owned by the source worker; successful notification never means mail delivery.
const SECRET = /^[a-f0-9]{64}$/;
const SCHEMA = 'anas.mail-notification/v1';
const encoder = new TextEncoder();
const hex = (bytes) =>
  Array.from(new Uint8Array(bytes), (value) => value.toString(16).padStart(2, '0')).join('');

export function notificationConfiguration(env) {
  if (!env.NOTIFY_ORIGIN && !env.NOTIFY_TOKEN) return { state: 'unconfigured' };
  try {
    const url = new URL(env.NOTIFY_ORIGIN);
    if (
      url.protocol !== 'https:' ||
      url.username ||
      url.password ||
      url.pathname !== '/' ||
      url.search ||
      url.hash ||
      !SECRET.test(env.NOTIFY_TOKEN) ||
      env.NOTIFY_TOKEN === env.READER_TOKEN ||
      env.NOTIFY_TOKEN === env.CONTROL_TOKEN
    )
      return { state: 'configuration-error' };
    return { state: 'configured', origin: url.origin, token: env.NOTIFY_TOKEN };
  } catch {
    return { state: 'configuration-error' };
  }
}
export async function notificationBinding(config) {
  return hex(
    await crypto.subtle.digest('SHA-256', encoder.encode(config.origin + '\0' + config.token))
  );
}
// This fixed receipt has four strings and one boolean. Scan its object keys
// before accepting JSON.parse's result, which otherwise hides duplicate keys.
function receiptFields(raw) {
  const string = /"(?:[^"\\\u0000-\u001f]|\\(?:["\\/bfnrt]|u[0-9a-fA-F]{4}))*"/y;
  const keys = new Set(['schema', 'source_id', 'generation', 'notification_id', 'accepted']);
  const seen = new Set();
  let pos = 0;
  const space = () => { while (/[ \t\r\n]/.test(raw[pos] ?? '')) pos++; };
  const readString = () => {
    string.lastIndex = pos;
    const match = string.exec(raw);
    if (!match) return null;
    pos = string.lastIndex;
    return match[0];
  };
  space();
  if (raw[pos++] !== '{') return false;
  space();
  while (raw[pos] !== '}') {
    const key = readString();
    if (key === null) return false;
    const name = JSON.parse(key);
    if (!keys.has(name) || seen.has(name)) return false;
    seen.add(name);
    space();
    if (raw[pos++] !== ':') return false;
    space();
    if (name === 'accepted') {
      if (raw.slice(pos, pos + 4) !== 'true') return false;
      pos += 4;
    } else if (readString() === null) return false;
    space();
    if (raw[pos] === ',') { pos++; space(); }
    else if (raw[pos] !== '}') return false;
  }
  pos++;
  space();
  return pos === raw.length && seen.size === keys.size;
}
async function replyJSON(response) {
  if (
    response.headers.get('Content-Type')?.split(';')[0].trim().toLowerCase() !== 'application/json'
  )
    return null;
  const reader = response.body?.getReader();
  if (!reader) return null;
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      size += next.value.byteLength;
      if (size > 1024) {
        await reader.cancel();
        return null;
      }
      chunks.push(next.value);
    }
    const raw = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      raw.set(chunk, offset);
      offset += chunk.byteLength;
    }
    const text = new TextDecoder('utf-8', { fatal: true }).decode(raw);
    const result = JSON.parse(text);
    return receiptFields(text) ? result : null;
  } catch {
    return null;
  }
}
export async function sendNotification(config, notice, now) {
  const payload = {
    schema: SCHEMA,
    source_id: notice.source_id,
    generation: notice.generation,
    notification_id: notice.notification_id,
    message_id: notice.message_id,
    mailbox_id: notice.mailbox_id,
  };
  const body = JSON.stringify(payload),
    timestamp = String(Math.floor(now / 1000));
  const secret = Uint8Array.from(config.token.match(/../g), (value) => parseInt(value, 16));
  const key = await crypto.subtle.importKey(
    'raw',
    secret,
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign']
  );
  const signature = hex(
    await crypto.subtle.sign('HMAC', key, encoder.encode(timestamp + '.' + body))
  );
  const signal = AbortSignal.timeout(5000);
  let response;
  try {
    response = await fetch(config.origin + '/api/ingress/notify', {
      method: 'POST',
      redirect: 'manual',
      signal,
      headers: {
        'Content-Type': 'application/json',
        'X-ANAS-Mail-Time': timestamp,
        'X-ANAS-Mail-Signature': signature,
      },
      body,
    });
  } catch {
    return { state: 'failed', check: signal.aborted ? 'timeout' : 'unreachable', http_status: 0 };
  }
  const http_status = response.status;
  if (http_status === 200 || http_status === 202) {
    const result = await replyJSON(response);
    if (
      result &&
      Object.keys(result).sort().join(',') ===
        'accepted,generation,notification_id,schema,source_id' &&
      result.schema === SCHEMA &&
      result.source_id === payload.source_id &&
      result.generation === payload.generation &&
      result.notification_id === payload.notification_id &&
      result.accepted === true
    )
      return {
        state: 'notified',
        check: '',
        http_status,
        notified_at: new Date(now).toISOString(),
      };
    return { state: 'configuration-error', check: 'invalid-receipt', http_status };
  }
  return {
    state:
      http_status >= 300 && http_status < 500 && ![408, 429].includes(http_status)
        ? 'configuration-error'
        : 'failed',
    check: 'http-response',
    http_status,
  };
}
