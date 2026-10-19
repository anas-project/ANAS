"""System-managed primary addresses; no mailbox state or address overrides."""
from __future__ import annotations

import ipaddress
import re

LOCAL = re.compile(r"[A-Za-z0-9_][A-Za-z0-9._-]{0,63}\Z", re.ASCII)
LABEL = re.compile(r"[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\Z", re.ASCII)


def mail_domain(value: str) -> str:
    value = value.strip().lower().removesuffix('.')
    if not value or len(value) > 253 or any(not LABEL.fullmatch(label) for label in value.split('.')):
        raise ValueError('invalid primary email domain')
    try:
        ipaddress.ip_address(value)
    except ValueError:
        return value
    raise ValueError('primary email domain cannot be an IP address')


def default_address(username: str, domain: str) -> bytes:
    name = username.lower()
    if not LOCAL.fullmatch(username) or '..' in name or name.endswith('.'):
        raise ValueError('username cannot form a default email address')
    address = name + '@' + mail_domain(domain)
    if len(address) > 254:
        raise ValueError('default email address is too long')
    return address.encode('ascii')


def values(attributes: dict, name: str) -> list[bytes]:
    return next((value for key, value in attributes.items() if key.casefold() == name.casefold()), [])


def desired_mail(attributes: dict, domain: str) -> bytes | None:
    classes = {item.lower() for item in values(attributes, 'objectClass')}
    if b'user' not in classes or b'computer' in classes:
        return None
    flags = values(attributes, 'userAccountControl')
    if len(flags) != 1:
        raise ValueError('personnel account flags unavailable')
    if int(flags[0]) & 0x3800:
        return None
    sid = values(attributes, 'objectSid')
    if sid and len(sid[0]) >= 12 and int.from_bytes(sid[0][-4:], 'little') in (500, 501, 502):
        return None
    names = values(attributes, 'sAMAccountName')
    if len(names) != 1:
        raise ValueError('personnel username unavailable')
    username = names[0].decode('ascii')
    if username.casefold().startswith('krbtgt'):
        return None
    return default_address(username, domain)


def mail_occupants(entries) -> dict[bytes, set[bytes]]:
    occupied: dict[bytes, set[bytes]] = {}
    for _dn, attributes in entries:
        guids = values(attributes, 'objectGUID')
        if len(guids) != 1 or len(guids[0]) != 16:
            raise ValueError('mail namespace identity unavailable')
        addresses = list(values(attributes, 'mail'))
        addresses += [value[5:] for value in values(attributes, 'proxyAddresses') if value[:5].lower() == b'smtp:']
        for address in addresses:
            occupied.setdefault(address.lower(), set()).add(guids[0])
    return occupied


def plan_mail(people, namespace, domain: str):
    occupied = mail_occupants(namespace)
    planned = []
    for dn, attributes in people:
        address = desired_mail(attributes, domain)
        if address is None:
            continue
        guids = values(attributes, 'objectGUID')
        if len(guids) != 1 or len(guids[0]) != 16:
            raise ValueError('personnel identity unavailable')
        if occupied.get(address.lower(), set()) - {guids[0]}:
            raise ValueError('primary email address conflicts with another directory entry')
        occupied.setdefault(address.lower(), set()).add(guids[0])
        if values(attributes, 'mail') != [address]:
            planned.append((dn, attributes, address))
    return planned
