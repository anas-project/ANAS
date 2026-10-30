# TEST_CASES: MAILMOD-T-007
"""Bounded message reconstruction for the approved isolated experiment only.

No product entrypoint, network access, receipts, or cloud acknowledgements.
Unsupported syntax or Milter operations stop the experiment.
"""
import re
import struct

MAX_BYTES = 1 << 20
NAME = re.compile(rb"[A-Za-z0-9!#$%&'*+.^_`|~-]+")


def split_message(raw):
    if len(raw) > MAX_BYTES or b'\0' in raw or b'\r\n\r\n' not in raw:
        raise ValueError('invalid bounded message')
    head, body = raw.split(b'\r\n\r\n', 1)
    if len(head) > 262144 or b'\r' in head.replace(b'\r\n', b'') or b'\n' in head.replace(b'\r\n', b''):
        raise ValueError('unsupported header line endings')
    headers = []
    for line in head.split(b'\r\n'):
        if line.startswith((b' ', b'\t')):
            if not headers:
                raise ValueError('orphan header continuation')
            headers[-1] += b'\r\n' + line
        else:
            name, colon, _ = line.partition(b':')
            if not colon or not NAME.fullmatch(name):
                raise ValueError('unsupported header name')
            headers.append(line)
    if not headers or len(headers) > 512:
        raise ValueError('invalid header count')
    return headers, body


def header_frame(payload, leading_space):
    parts = payload.split(b'\0')
    if len(parts) != 3 or parts[-1] or not NAME.fullmatch(parts[0]):
        raise ValueError('invalid header frame')
    name, value, _ = parts
    if b'\r' in value or any(not line.startswith((b' ', b'\t')) for line in value.split(b'\n')[1:]):
        raise ValueError('header frame injection')
    value = value.replace(b'\n', b'\r\n')
    return name + b':' + (b'' if leading_space else b' ') + value


def render(headers, body):
    raw = b'\r\n'.join(headers) + b'\r\n\r\n' + body
    split_message(raw)
    return raw


def reconstruct(original, frames, body, changes, *, upstream_leading_space=False):
    headers = [header_frame(frame, True) for frame in frames]
    if render(headers, body) != original:
        raise ValueError('Milter input differs from private original')
    replacement = None
    for tag, payload in changes:
        if tag == 'b':
            replacement = (replacement or b'') + payload
            if len(replacement) > MAX_BYTES:
                raise ValueError('replacement body exceeds bound')
            continue
        if tag not in ('h', 'i', 'm'):
            raise ValueError('unsupported filter operation')
        index = None
        if tag != 'h':
            if len(payload) < 4:
                raise ValueError('missing header index')
            index, = struct.unpack('!I', payload[:4])
            payload = payload[4:]
        value = header_frame(payload, upstream_leading_space)
        name, _, field = payload.partition(b'\0')
        # A filter may neither change nor add the private correlation field.
        if name.lower() == b'x-anas-ingress-key':
            raise ValueError('filter changed private intent')
        if tag == 'h':
            headers.append(value)
        elif tag == 'i':
            # Indexing that might include Postfix's hidden Received is not
            # silently interpreted. It needs a separately validated mapping.
            raise ValueError('insert header requires hidden-header index mapping')
        else:
            if index == 0:
                index = 1
            positions = [i for i, h in enumerate(headers) if h.partition(b':')[0].lower() == name.lower()]
            if index > len(positions):
                # Postfix ignores deletion of an absent header. Adding through
                # CHGHEADER is deliberately unsupported in this first stage.
                if field == b'\0':
                    continue
                raise ValueError('change targets absent header')
            if field == b'\0':
                del headers[positions[index - 1]]
            else:
                headers[positions[index - 1]] = value
    return render(headers, body if replacement is None else replacement)


def bind_delivery(incoming, filtered, replacement_received):
    headers, body = split_message(incoming)
    expected_headers, expected_body = split_message(filtered)
    # Exactly one extra leading header is permitted, and it is never passed to
    # Sieve. All other bytes (including original Received headers) must match.
    if len(headers) != len(expected_headers) + 1 or not headers[0].startswith(b'Received: ') or headers[1:] != expected_headers or body != expected_body:
        raise ValueError('LMTP content differs from verified filtered copy')
    supplied, supplied_body = split_message(replacement_received + b'\r\n\r\n')
    if len(supplied) != 1 or supplied_body or not supplied[0].startswith(b'Received: by anas-controlled-intake '):
        raise ValueError('invalid private replacement header')
    return render(supplied + expected_headers, expected_body)
