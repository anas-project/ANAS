package Lemonldap::NG::Portal::Plugins::AnasDirectory;

use strict;
use Mouse;
use Net::LDAP::Util qw(escape_filter_value);
use Lemonldap::NG::Portal::Lib::Net::LDAP;
use Lemonldap::NG::Portal::Main::Constants qw(PE_OK PE_ERROR);
extends 'Lemonldap::NG::Portal::Main::Plugin';

# Read authority directly. No shadow user database and no cached permission
# decision: a failed LDAP query must not turn a previous grant into a new one.
sub profile {
    my ($self, $anchor) = @_;
    die "invalid directory identity\n" unless defined($anchor)
        && $anchor =~ /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
    my $ldap = Lemonldap::NG::Portal::Lib::Net::LDAP->new({p => $self->p, conf => $self->conf});
    die "directory unavailable\n" unless $ldap && !$ldap->bind->code;
    my %vars = (%{$self->conf->{exportedVars} // {}}, %{$self->conf->{ldapExportedVars} // {}});
    my %attrs = map { $_ => 1 } (values %vars, qw(anasIdentityAnchor sAMAccountName userAccountControl msDS-User-Account-Control-Computed accountExpires));
    my $result = $ldap->search(base => $self->conf->{ldapBase}, scope => 'sub',
        filter => '(&(objectClass=user)(anasIdentityAnchor=' . escape_filter_value($anchor) . '))',
        attrs => [sort keys %attrs]);
    die "directory search failed\n" if $result->code || $result->count > 1;
    unless ($result->count == 1) { $ldap->unbind; return; }
    my $entry = $result->entry(0);
    my $profile = {};
    for my $key (keys %vars) {
        my $value = $ldap->getLdapValue($entry, $vars{$key});
        $profile->{$key} = length($value) ? $value : undef;
    }
    $profile->{anasIdentityAnchor} = $entry->get_value('anasIdentityAnchor');
    $profile->{sAMAccountName} = $entry->get_value('sAMAccountName');
    $profile->{_dn} = $entry->dn;
    $profile->{anasAccountControl} = $entry->get_value('userAccountControl');
    $profile->{anasAccountState} = $entry->get_value('msDS-User-Account-Control-Computed');
    $profile->{anasAccountExpires} = $entry->get_value('accountExpires');
    # Matching-rule-in-chain resolves direct and nested groups in Samba AD.
    my $groups = $ldap->search(base => $self->conf->{ldapGroupBase}, scope => 'sub',
        filter => '(&(objectClass=group)(member:1.2.840.113556.1.4.1941:='
            . escape_filter_value($entry->dn) . '))', attrs => ['cn']);
    die "directory group search failed\n" if $groups->code;
    my %names;
    for my $group ($groups->entries) {
        my $name = $group->get_value('cn');
        die "ambiguous directory group\n" unless defined($name) && length($name) && !$names{$name};
        $names{$name} = {cn => [$name], dn => [$group->dn]};
    }
    $ldap->unbind;
    $profile->{hGroups} = \%names;
    $profile->{groups} = join($self->conf->{multiValuesSeparator}, sort keys %names);
    return $profile;
}

sub enabled {
    my ($self, $profile, $now) = @_;
    return 0 unless $profile;
    for my $key (qw(anasAccountControl anasAccountState anasAccountExpires)) {
        return 0 unless defined($profile->{$key}) && $profile->{$key} =~ /^[0-9]+$/;
    }
    return 0 if ($profile->{anasAccountControl} & 2) || ($profile->{anasAccountState} & 16);
    return !$profile->{anasAccountExpires}
        || $profile->{anasAccountExpires} > (($now // time) + 11644473600) * 10000000;
}

sub refresh {
    my ($self, $req) = @_;
    my $profile = eval { $self->profile($req->sessionInfo->{anasIdentityAnchor}) };
    if ($@ || !$self->enabled($profile)) {
        $self->logger->warn('ANAS directory admission denied');
        return PE_ERROR;
    }
    # Only directory attributes change. Keep authentication time, factors and
    # protocol session identifiers from the authenticated LLNG session.
    for my $key (keys %$profile) {
        $req->sessionInfo->{$key} = $profile->{$key};
        $req->userData->{$key} = $profile->{$key};
    }
    # Directory-derived role macros must use the new groups as well.
    $self->p->setMacros($req);
    for my $key (keys %{$self->p->_macros}) {
        $req->userData->{$key} = $req->sessionInfo->{$key};
    }
    return PE_OK;
}

1;
