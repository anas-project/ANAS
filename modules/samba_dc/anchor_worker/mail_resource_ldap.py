"""python-ldap adapter; uses the worker's existing verified LDAPS connection."""
from mail_policy import values
from mail_resources import FIELDS


class LDAPResources:
    def __init__(self, directory):
        import ldap
        from ldap.controls import LDAPControl
        self.directory, self.ldap = directory, ldap
        self.scope = LDAPControl('1.2.840.113556.1.4.1339', criticality=True)

    def read(self, dn):
        try:
            rows = self.directory._retry(lambda c: c.search_ext_s(
                dn, self.ldap.SCOPE_BASE, '(objectClass=*)', list(FIELDS),
                serverctrls=[self.scope], timeout=15))
        except self.ldap.NO_SUCH_OBJECT:
            return None
        if len(rows) != 1 or not rows[0][0]:
            raise ValueError('mail primary ambiguous resource')
        return rows[0][1]

    def installed(self):
        c = self.directory.connect()
        root = c.search_s('', self.ldap.SCOPE_BASE, '(objectClass=*)', ['schemaNamingContext'])
        if len(root) != 1 or len(values(root[0][1], 'schemaNamingContext')) != 1:
            raise ValueError('mail primary schema scope unavailable')
        schema = values(root[0][1], 'schemaNamingContext')[0].decode()
        rows = self.directory._retry(lambda c: c.search_ext_s(
            schema, self.ldap.SCOPE_SUBTREE, '(lDAPDisplayName=anasMailbox)', ['lDAPDisplayName'], timeout=15))
        if len(rows) > 1 or any(not dn for dn, _attrs in rows):
            raise ValueError('mail primary schema ambiguous')
        return bool(rows)

    def add(self, dn, attrs):
        try:
            # Mutations never use automatic transport retry. A lost response
            # leaves durable intent; the next reconciliation reads it first.
            self.directory.connect().add_s(dn, list(attrs.items()))
        except self.ldap.ALREADY_EXISTS:
            pass

    def cas(self, dn, before, changes):
        version = values(before, 'anasMailConfigVersion')
        if len(version) != 1:
            raise ValueError('mail primary version missing')
        # Samba's native strict Delete-old/Add-new is already exercised by the
        # portal. Do not rely on RFC4528, which AD does not advertise.
        modifications = [(self.ldap.MOD_DELETE, 'anasMailConfigVersion', version),
                         (self.ldap.MOD_ADD, 'anasMailConfigVersion', changes['anasMailConfigVersion'])]
        modifications += [(self.ldap.MOD_REPLACE, name, value) for name, value in changes.items() if name != 'anasMailConfigVersion']
        self.directory.connect().modify_s(dn, modifications)

    def person(self, guid):
        return self.directory.read_target('<GUID=' + guid + '>')

    def in_scope(self, dn):
        return self.directory.mail_in_scope(dn)

    def entries(self, filterstr):
        yield from self.directory.iter_entries(self.directory.settings.mail_search_base, filterstr, domain_scope=True)

    def unique_person(self, owner, guid):
        import uuid
        rows = list(self.entries('(anasIdentityAnchor=' + self.ldap.filter.escape_filter_chars(owner) + ')'))
        if len(rows) != 1 or values(rows[0][1], 'objectGUID') != [uuid.UUID(guid).bytes_le]:
            raise ValueError('mail primary owner anchor not unique')

    def namespace(self, email, guid):
        import uuid
        # A broad domain search also catches legacy values with whitespace.
        # Never treat unreadable/external partitions as an empty namespace.
        for _dn, attrs in self.entries('(|(mail=*)(proxyAddresses=*))'):
            found = [v.strip().lower() for v in values(attrs, 'mail')]
            found += [v.strip()[5:].strip().lower() for v in values(attrs, 'proxyAddresses') if v.strip()[:5].lower() == b'smtp:']
            if email.encode() in found and values(attrs, 'objectGUID') != [uuid.UUID(guid).bytes_le]:
                raise ValueError('mail primary address conflicts with directory entry')

    def update_person(self, dn, before, email, proxies):
        import uuid
        # A GUID locator prevents replacement at the same personnel DN from
        # receiving this operation. Compare writable identity/address preimages
        # with strict Delete/Add in ONE Modify; no permissive-modify control.
        changes = []
        for name in ('mS-DS-ConsistencyGuid', 'anasIdentityAnchor'):
            found = values(before, name)
            if len(found) != 1:
                raise ValueError('mail primary person comparison field missing')
            changes += [(self.ldap.MOD_DELETE, name, found), (self.ldap.MOD_ADD, name, found)]
        guid = values(before, 'objectGUID')
        if len(guid) != 1 or len(guid[0]) != 16:
            raise ValueError('mail primary person comparison GUID missing')
        for name, wanted in (('mail', [email]), ('proxyAddresses', proxies)):
            previous = values(before, name)
            if previous:
                changes.append((self.ldap.MOD_DELETE, name, previous))
            changes.append((self.ldap.MOD_ADD, name, wanted))
        self.directory.connect().modify_s('<GUID=' + str(uuid.UUID(bytes_le=guid[0])) + '>', changes)
