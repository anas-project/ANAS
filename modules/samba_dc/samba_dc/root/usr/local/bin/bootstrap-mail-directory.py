#!/usr/bin/python3
"""Managed, offline bootstrap for the confirmed mail resource design.

The caller supplies a verified full snapshot reference and stops external
writers before enabling. This command never changes an existing role's SID,
resets a password, repairs foreign ACLs or reruns the old-domain import after
the completion marker exists.
"""
import importlib.util
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import uuid

MARKER = Path("/var/lib/samba/.anas-mail-directory-v1.json")
EVIDENCE = Path("/var/lib/anas-mail-bootstrap")
SERVICE_MARKER = "ANAS mail service/v1 "


def module(name):
    spec = importlib.util.spec_from_file_location(name.replace("-", "_"), Path(__file__).with_name(name + ".py"))
    result = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(result)
    return result


def private_json(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or stat.S_IMODE(info.st_mode) != 0o600 or info.st_nlink != 1 or info.st_uid != os.geteuid() or info.st_size > 65536:
        raise ValueError("mail bootstrap unsafe completion marker")
    with path.open("r") as stream:
        opened = os.fstat(stream.fileno())
        if (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino):
            raise ValueError("mail bootstrap completion marker changed")
        return json.load(stream)


def save_marker(path, value):
    temp = path.with_name(path.name + "." + uuid.uuid4().hex)
    try:
        with open(temp, "x", opener=lambda p, f: os.open(p, f, 0o600)) as stream:
            json.dump(value, stream, sort_keys=True)
            stream.write("\n")
            stream.flush(); os.fsync(stream.fileno())
        os.replace(temp, path)
        fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
        try: os.fsync(fd)
        finally: os.close(fd)
    finally:
        temp.unlink(missing_ok=True)


def complete(config, existing, roles):
    expected = {"schema": "anas.mail-bootstrap/v1", "source_domain": config["mail_domain"],
                "snapshot_id": config["snapshot_id"], "role_dns": config["roles"], "role_sids": roles}
    if existing is not None and existing != expected:
        raise ValueError("mail bootstrap completed identity or initial baseline changed")
    return expected


def execute(config, backend, marker=MARKER):
    existing = private_json(marker) if marker.exists() or marker.is_symlink() else None
    backend.preflight()
    if existing is None:
        backend.schema(True)
        backend.ensure_services()
        backend.prepare(True)
        roles = backend.roles()
        backend.migrate()
        backend.prepare(False)
        state = complete(config, None, roles)
        save_marker(marker, state)
    else:
        backend.schema(False)
        backend.prepare(False)
        complete(config, existing, backend.roles())
    return existing is None


class Native:
    def __init__(self, config, secrets):
        self.config, self.secrets = config, secrets
        self.installer = module("install-mail-directory")
        self.directory = self.installer.Directory()

    def preflight(self):
        if os.geteuid() != 0 or os.environ.get("SAMBA_DC_DOMAIN_ACTION") == "join":
            raise ValueError("mail bootstrap requires root on the single writable DC")
        for process in ("samba", "samba-ad-dc"):
            if subprocess.run(["pgrep", "-x", process], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode != 1:
                raise ValueError("mail bootstrap requires a stopped DC and external writers")
        d = self.directory
        if d.dn(self.config["base_dn"]) != d.dn(str(d.db.domain_dn())):
            raise ValueError("mail bootstrap domain mismatch")
        service = d.dn("OU=Service Accounts," + self.config["base_dn"])
        dns = [d.dn(dn) for dn in self.config["roles"].values()]
        if len({str(dn).casefold() for dn in dns}) != 4 or any(dn.parent() != service for dn in dns):
            raise ValueError("mail bootstrap distinct direct service roles required")
        root = d.read("", ["configurationNamingContext", "dsServiceName"])
        nodes = d.db.search(base=str(root["configurationNamingContext"][0]), expression="(objectClass=nTDSDSA)", attrs=["msDS-isRODC"])
        schema = d.read(str(d.db.get_schema_basedn()), ["fSMORoleOwner"])
        if len(nodes) != 1 or str(nodes[0].get("msDS-isRODC", ["FALSE"])[0]).upper() != "FALSE" or d.dn(str(schema["fSMORoleOwner"][0])) != nodes[0].dn or d.dn(str(root["dsServiceName"][0])) != nodes[0].dn:
            raise ValueError("mail bootstrap requires the single writable schema-master DC")

    def schema(self, write):
        schema = module("install-mail-schema")
        schema.install(schema.Directory(), schema.load_spec(), write)
        # Reload after schema additions so the native SamDB sees both classes.
        self.directory = self.installer.Directory()

    def ensure_services(self):
        d, base = self.directory, self.config["base_dn"]
        d.begin()
        try:
            for dn in (self.config["people_dn"], "OU=Service Accounts," + base):
                row = d.read(dn, ["objectClass"])
                if row is None:
                    d.db.add({"dn": dn, "objectClass": "organizationalUnit"})
                elif b"organizationalUnit" not in row["objectClass"]:
                    raise ValueError("mail bootstrap provider OU conflict")
            service = d.dn("OU=Service Accounts," + base)
            for role, dn in self.config["roles"].items():
                parsed = d.dn(dn)
                if parsed.parent() != service:
                    raise ValueError("mail bootstrap invalid service scope")
                row = d.read(dn, ["objectClass", "description"])
                if row is None:
                    name = parsed.get_rdn_value()
                    # No shell/argv password transport; native local AD API only.
                    d.db.newuser(name, self.secrets[role], userou="OU=Service Accounts", description=SERVICE_MARKER + role)
                    change = d.ldb.Message(); change.dn = parsed
                    change["userAccountControl"] = d.ldb.MessageElement("66048", d.ldb.FLAG_MOD_REPLACE, "userAccountControl")
                    d.db.modify(change)
                elif role in ("creator", "address_writer") and list(row.get("description", [])) != [(SERVICE_MARKER + role).encode()]:
                    raise ValueError("mail bootstrap foreign service account must not be adopted")
            d.commit()
        except BaseException:
            d.cancel(); raise

    def directory_config(self):
        return {key: self.config[key] for key in ("base_dn", "people_dn", "roles")}

    def prepare(self, write):
        self.installer.prepare(self.directory, self.directory_config(), write)

    def roles(self):
        self.directory.begin()
        try:
            sids, _ = self.directory.validate(self.directory_config())
            return sids
        finally: self.directory.cancel()

    def migrate(self):
        EVIDENCE.mkdir(mode=0o700, exist_ok=True)
        info = EVIDENCE.lstat()
        # Docker can create a new bind source as an empty root-owned 0755
        # directory. Tighten that empty directory before writing any evidence.
        if stat.S_ISDIR(info.st_mode) and info.st_uid == os.geteuid() and stat.S_IMODE(info.st_mode) == 0o755 and not any(EVIDENCE.iterdir()):
            EVIDENCE.chmod(0o700)
            info = EVIDENCE.lstat()
        if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.geteuid() or stat.S_IMODE(info.st_mode) != 0o700:
            raise ValueError("mail bootstrap private evidence mount required")
        source = {key: self.config[key] for key in ("base_dn", "people_dn", "roles", "mail_domain")}
        command = [sys.executable, str(Path(__file__).with_name("migrate-mail-primary.py"))]
        checked = subprocess.run(command + ["--check"], input=json.dumps(source), text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        if checked.returncode or len(checked.stdout) > 65536 or json.loads(checked.stdout).get("unmapped_smtp_count") != 0:
            raise ValueError("mail bootstrap unresolved legacy SMTP addresses require explicit mapping")
        args = command + ["--execute", "--snapshot-id", self.config["snapshot_id"], "--evidence-dir", str(EVIDENCE / ("attempt-" + uuid.uuid4().hex))]
        result = subprocess.run(args, input=json.dumps(source), text=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if result.returncode:
            raise ValueError("mail bootstrap primary import failed; inspect private migration evidence")


def configuration(env):
    base = env["SAMBA_DC_BASE_DN"]
    config = {"base_dn": base, "people_dn": env["SAMBA_DC_BASE_USERS_DN"], "mail_domain": env["SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN"], "snapshot_id": env["SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID"], "roles": {}}
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._:-]{0,127}", config["snapshot_id"]):
        raise ValueError("mail bootstrap verified snapshot reference required")
    # Use the exact existing domain parser rather than accepting URL/email input.
    sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "share/anas"))
    from mail_policy import mail_domain
    if mail_domain(config["mail_domain"]) != config["mail_domain"]:
        raise ValueError("mail bootstrap canonical source domain required")
    secrets = {}
    for role, prefix in {"reader": "SAMBA_DC_LDAP_BIND", "creator": "SAMBA_DC_MAIL_CREATOR_BIND", "address_writer": "SAMBA_DC_MAIL_ADDRESS_BIND", "primary_writer": "SAMBA_DC_ANCHOR_BIND"}.items():
        config["roles"][role] = env[prefix + "_DN"]
        secrets[role] = env[prefix + "_PASSWORD"]
        if not secrets[role] or any(c in secrets[role] for c in "\x00\r\n"):
            raise ValueError("mail bootstrap private service credentials required")
    return config, secrets


def main():
    if os.environ.get("SAMBA_DC_MAIL_DIRECTORY_ENABLED", "false") == "false": return 0
    try:
        if os.environ.get("SAMBA_DC_MAIL_DIRECTORY_ENABLED") != "true": raise ValueError("mail bootstrap switch invalid")
        config, secrets = configuration(os.environ)
        created = execute(config, Native(config, secrets))
        print("mail bootstrap verified; initial_import=" + str(created).lower())
        return 0
    except Exception:
        # Never echo AD errors, environment values or passwords.
        print("mail bootstrap failed; keep directory/mail disabled and inspect private baseline", file=sys.stderr)
        return 1


if __name__ == "__main__": sys.exit(main())
