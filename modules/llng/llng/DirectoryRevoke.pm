package Anas::LLNG::DirectoryRevoke;

use strict;
use warnings;
use JSON qw(encode_json decode_json);
use Digest::SHA qw(sha256_hex);
use Lemonldap::NG::Portal;
use Lemonldap::NG::Portal::Plugins::AnasDirectory;
use Lemonldap::NG::Common::Apache::Session;
use Lemonldap::NG::Common::Session;
use Lemonldap::NG::Portal::Main::Request;

my $json = JSON->new->canonical;

sub new {
    my ($class, %args) = @_;
    my $p = $args{portal};
    my $oidc = $p->loadedModules->{'Lemonldap::NG::Portal::Issuer::OpenIDConnect'};
    $oidc->ua->timeout(5);
    $oidc->ua->max_redirect(0);
    $oidc->ua->add_handler(response_done => sub {
        my ($response) = @_;
        # Native LLNG treats redirects as success. A directory revocation must
        # receive an explicit acknowledgement from the registered application.
        $response->code(502) unless $response->code == 200 || $response->code == 204;
    });
    return bless {%args, directory => Lemonldap::NG::Portal::Plugins::AnasDirectory->new(p => $p, conf => $p->conf)}, $class;
}

sub request {
    my ($self, $data) = @_;
    my $req = Lemonldap::NG::Portal::Main::Request->new({REQUEST_METHOD => 'POST',
        SERVER_NAME => 'directory-worker', SERVER_PORT => 80, PATH_INFO => '/', REQUEST_URI => '/',
        'psgi.url_scheme' => 'http'});
    $req->init($self->{portal}->conf);
    $req->portal($self->{portal}->conf->{portal});
    $req->sessionInfo({%$data});
    $req->userData({%$data});
    return $req;
}

sub sessions {
    my ($self, $store) = @_;
    my $conf = $self->{portal}->conf;
    my $sessions = Lemonldap::NG::Common::Apache::Session->get_key_from_all_sessions({
        %{$conf->{$store . 'StorageOptions'}}, backend => $conf->{$store . 'Storage'}});
    die "session enumeration failed\n" unless ref($sessions) eq 'HASH';
    return $sessions;
}

sub session {
    my ($self, $store, $id) = @_;
    my $conf = $self->{portal}->conf;
    my $session = Lemonldap::NG::Common::Session->new(id => $id, hashStore => 0,
        storageModule => $conf->{$store . 'Storage'},
        storageModuleOptions => {%{$conf->{$store . 'StorageOptions'}}});
    if ($session->error) {
        return if $session->error =~ /Object does not exist/;
        die "session storage unavailable\n";
    }
    return $session;
}

# Compare only the attributes a client consumes. Group membership alone does
# not evict another client whose admission and role are unchanged.
sub affected {
    my ($self, $rp, $old, $current) = @_;
    return 1 unless $self->{directory}->enabled($current);
    my $p = $self->{portal};
    my $options = $p->conf->{oidcRPMetaDataOptions}->{$rp};
    return 1 unless $options;
    my $rule = $p->buildRule($options->{oidcRPMetaDataOptionsRule}, 'directory event admission');
    die "invalid current client admission rule\n" unless $rule;
    return 1 unless $rule->($self->request($current), $current);
    # Native mappings may append type/cardinality, e.g. groups;string;always.
    my @sources = map {(split /;/, $_)[0]} values %{$p->conf->{oidcRPMetaDataExportedVars}->{$rp} // {}};
    my $admin = $self->{admin_group};
    return 1 if (grep {$_ eq 'groups' || $_ eq 'anasRole'} @sources)
        && $old->{hGroups}->{$admin} && !$current->{hGroups}->{$admin};
    for my $source (@sources) {
        next if $source eq 'groups';
        return 1 if ($old->{$source} // '') ne ($current->{$source} // '');
    }
    return 0;
}

sub notification {
    my ($self, $rp, $data) = @_;
    my $oidc = $self->{portal}->loadedModules->{'Lemonldap::NG::Portal::Issuer::OpenIDConnect'};
    my $options = $self->{portal}->conf->{oidcRPMetaDataOptions}->{$rp};
    my $parents = decode_json($self->{portal}->conf->{customPluginsParams}->{anasReauthParents} // '{}');
    if (my $parent = $parents->{$options->{oidcRPMetaDataOptionsClientID}}) {
        $rp = $oidc->getRP($parent);
        die "reauthentication parent unavailable\n" unless $rp;
        $options = $self->{portal}->conf->{oidcRPMetaDataOptions}->{$rp};
        die "reauthentication parent requires unsupported session logout\n"
            if $options->{oidcRPMetaDataOptionsLogoutSessionRequired};
    }
    my $info = {anasIdentityAnchor => $data->{anasIdentityAnchor},
        _oidc_logout_sub => $data->{anasIdentityAnchor},
        _oidc_sid => $oidc->getSidFromSession($rp, $data)};
    return {rp => $rp, client => $options->{oidcRPMetaDataOptionsClientID}, session => $info};
}

# Produce a durable plan before touching any grant. No directory shadow state is
# retained: each batch re-reads AD for identities with active SSO/offline grants.
sub plan {
    my ($self) = @_;
    my $p = $self->{portal};
    my $oidc = $p->loadedModules->{'Lemonldap::NG::Portal::Issuer::OpenIDConnect'};
    my $sso = $self->sessions('global');
    my $tokens = $self->sessions('oidc');
    my (%profiles, %actions, %revoked, %deleted);
    my $profile = sub {
        my ($anchor) = @_;
        return $profiles{$anchor} if exists $profiles{$anchor};
        my $current = $self->{directory}->profile($anchor);
        if ($current) {
            my $req = $self->request($current);
            $p->setMacros($req);
            $current = $req->sessionInfo;
        }
        return $profiles{$anchor} = $current;
    };
    for my $id (sort keys %$sso) {
        my $old = $sso->{$id};
        next unless ($old->{_session_kind} // '') eq 'SSO';
        my $anchor = $old->{anasIdentityAnchor};
        unless ($anchor) {
            $actions{'global:' . $id} = {type => 'remove', store => 'global', id => $id};
            $deleted{$id} = 1;
            $actions{'legacy:' . $id} = {type => 'unsupported', protocol => 'legacy-identity'}
                if $old->{_oidcConnectedRPIDs} || $old->{_lassoSessionDumpI};
            next;
        }
        my $current = $profile->($anchor);
        my $enabled = $self->{directory}->enabled($current);
        my @remove;
        for my $client (grep {length} split /,/, ($old->{_oidcConnectedRPIDs} // '')) {
            my $rp = $oidc->getRP($client);
            die "connected client configuration missing\n" unless $rp;
            if ($self->affected($rp, $old, $current)) {
                my $n = $self->notification($rp, {%$old, _session_storage_id => $id});
                $actions{'notify:' . sha256_hex($id . ':' . $client)} = {type => 'notify', %$n};
                $revoked{$id}->{$client} = 1;
                push @remove, $client;
            }
        }
        if (!$enabled) {
            $actions{'global:' . $id} = {type => 'remove', store => 'global', id => $id};
            $deleted{$id} = 1;
        } else {
            my %fields = %$current;
            # Protocol registrations belong to the native issuer, not LDAP.
            delete $fields{_oidcConnectedRPIDs};
            my %previous = map {$_ => $old->{$_}} keys %fields;
            $actions{'global:' . $id} = {type => 'update', store => 'global', id => $id,
                fields => \%fields, remove_clients => \@remove}
                if @remove || $json->encode(\%previous) ne $json->encode(\%fields);
        }
        # SAML SOAP support is handled separately. Never acknowledge an event
        # as fully revoked while a browser-only SP still owns a session.
        if ($old->{_lassoSessionDumpI} && (!$enabled || $json->encode($old->{hGroups} // {}) ne $json->encode($current->{hGroups} // {}))) {
            $actions{'saml:' . $id} = {type => 'unsupported', protocol => 'saml'};
        }
    }
    for my $id (sort keys %$tokens) {
        my $token = $tokens->{$id};
        my $client = $token->{client_id} // '';
        my $sessionID = $token->{user_session_id};
        my $storageID = $p->conf->{hashedSessionStore} && $sessionID
            ? Lemonldap::NG::Common::Session::id2storage($sessionID) : $sessionID;
        my $remove = $storageID && ($deleted{$storageID} || $revoked{$storageID}->{$client});
        # Offline tokens contain their own directory snapshot and native sid.
        if (!$sessionID && $token->{anasIdentityAnchor}) {
            my $rp = $oidc->getRP($client);
            die "offline client configuration missing\n" unless $rp;
            if ($self->affected($rp, $token, $profile->($token->{anasIdentityAnchor}))) {
                $remove = 1;
                $actions{'offline:' . $id} = {type => 'notify', %{$self->notification($rp, $token)}};
            }
        }
        $actions{'oidc:' . $id} = {type => 'remove', store => 'oidc', id => $id} if $remove;
    }
    return \%actions;
}

sub apply {
    my ($self, $action) = @_;
    if ($action->{type} eq 'notify') {
        my $p = $self->{portal};
        my $oidc = $p->loadedModules->{'Lemonldap::NG::Portal::Issuer::OpenIDConnect'};
        my $options = $p->conf->{oidcRPMetaDataOptions}->{$action->{rp}};
        die "logout client configuration changed\n" unless $options
            && ($options->{oidcRPMetaDataOptionsClientID} // '') eq $action->{client};
        unless (($options->{oidcRPMetaDataOptionsLogoutType} // '') eq 'back'
                && $options->{oidcRPMetaDataOptionsLogoutUrl}) {
            my %accepted = map {$_ => 1} grep {length} split /,/,
                ($p->conf->{customPluginsParams}->{anasBackgroundLogoutExceptions} // '');
            die "client has no background logout capability\n" unless $accepted{$action->{client}};
            # This is an acknowledged product limitation, never a successful
            # application logout. Native grants were removed before callbacks.
            return {limitation => $action->{client}};
        }
        # Preserve the native signature, audience, issuer and sid construction.
        die "application logout pending\n" if $oidc->_backChannelLogout(
            $self->request($action->{session}), $action->{rp}, $action->{session});
        return;
    }
    die "SAML application logout requires browser or SOAP support\n" if $action->{type} eq 'unsupported';
    my $session = $self->session($action->{store}, $action->{id});
    return unless $session; # Idempotent retry after a successful deletion.
    my $ok;
    if ($action->{type} eq 'remove') { $ok = $session->remove; }
    else {
        # Like native Session::update, re-tie without cache and merge while the
        # storage backend holds its native session lock. Never replace the RP
        # list with a snapshot taken before a concurrent application login.
        my $data = $session->_tie_session({noCache => 1});
        die "session mutation failed\n" unless $data;
        my %remove = map {$_ => 1} @{$action->{remove_clients} // []};
        $data->{$_} = $action->{fields}->{$_}
            for grep {$_ ne '_oidcConnectedRPIDs'} keys %{$action->{fields}};
        if (%remove) {
            $data->{_oidcConnectedRPIDs} = join(',', grep {!$remove{$_}}
                split /,/, ($data->{_oidcConnectedRPIDs} // ''));
        }
        untie %$data;
        $ok = 1;
    }
    die "session mutation failed\n" unless $ok;
}

1;
