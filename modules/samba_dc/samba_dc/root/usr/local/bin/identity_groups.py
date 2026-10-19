#!/usr/bin/python3
"""Reconcile only groups declared by the frozen deployment catalog.

System group descriptions are informational and never written to AD. Membership
and ACL setup remain explicit bootstrap policy, outside this metadata file.
"""
import json
import subprocess

import ldb
from samba.auth import system_session
from samba.param import LoadParm
from samba.samdb import SamDB


def text(value):
    return value.decode("utf-8") if isinstance(value, bytes) else str(value)


def search(db, **kwargs):
    try:
        return db.search(**kwargs)
    except ldb.LdbError as exc:
        if exc.args[0] == ldb.ERR_NO_SUCH_OBJECT:
            return []
        raise


def reconcile(db, catalog):
    if catalog.get("schema") != "anas.identity-groups/v1":
        raise ValueError("Unsupported identity group catalog")
    base = str(db.domain_dn())
    groups = catalog["groups"]
    seen = set()
    # Validate the entire batch before any directory writes.
    for group in groups:
        if not group["managed"]:
            continue
        dn = ldb.Dn(db, group["dn"])
        if not dn.is_child_of(ldb.Dn(db, "OU=Groups," + base)) or str(dn.get_rdn_value()) != group["name"]:
            raise ValueError("Managed group outside the group directory")
        key = str(dn).lower()
        if key in seen:
            raise ValueError("Duplicate group directory locator")
        seen.add(key)
        if not group["description"].get("en"):
            raise ValueError("Managed group has no English description")
    for group in groups:
        if not group["managed"]:
            continue
        dn = ldb.Dn(db, group["dn"])
        parent = dn.parent()
        ancestors = []
        while parent != ldb.Dn(db, base):
            ancestors.append(str(parent))
            parent = parent.parent()
        for ancestor in reversed(ancestors):
            if not search(db, base=ancestor, scope=ldb.SCOPE_BASE, expression="(objectClass=organizationalUnit)", attrs=["dn"]):
                subprocess.run(["samba-tool", "ou", "create", ancestor], check=True)
        found = search(db, base=str(dn), scope=ldb.SCOPE_BASE, attrs=["objectClass", "description"])
        description = group["description"]["en"]
        if not found:
            parent_relative = str(dn.parent())[: -(len(base) + 1)]
            subprocess.run(["samba-tool", "group", "add", group["name"], "--groupou=" + parent_relative, "--description=" + description], check=True)
        else:
            if "group" not in [text(v) for v in found[0]["objectClass"]]:
                raise ValueError("Group locator belongs to another object type")
            current = [text(v) for v in found[0].get("description", [])]
            if current != [description]:
                change = ldb.Message()
                change.dn = dn
                change["description"] = ldb.MessageElement(description, ldb.FLAG_MOD_REPLACE, "description")
                db.modify(change)


if __name__ == "__main__":
    path = "/run/anas/identity-groups/catalog.json"
    with open(path, encoding="utf-8") as source:
        catalog = json.load(source)
    lp = LoadParm()
    lp.load_default()
    db = SamDB(url="/var/lib/samba/private/sam.ldb", session_info=system_session(), lp=lp)
    reconcile(db, catalog)
