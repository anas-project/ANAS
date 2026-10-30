package Lemonldap::NG::Portal::Plugins::AnasOIDC;

use strict;
use Mouse;
use JSON qw(from_json);
use Digest::SHA qw(sha256_hex);
use Lemonldap::NG::Portal::Main::Constants qw(PE_OK PE_ERROR);
use Lemonldap::NG::Portal::Plugins::AnasDirectory;
extends 'Lemonldap::NG::Portal::Lib::OIDCPlugin';

# Native extension points: no replacement of upstream authentication or token
# validation. Mark only requests that actually completed a fresh authentication.
use constant afterData => 'authenticatedNow';
use constant endAuth => 'recordAuthentication';
use constant hook => { oidcGotRequest => 'checkAuthorization', samlGotAuthnRequest => 'checkDirectory' };

has directory => (is => 'ro', lazy => 1, default => sub {
    Lemonldap::NG::Portal::Plugins::AnasDirectory->new(p => $_[0]->p, conf => $_[0]->conf);
});
has accountPolicies => (is => 'rw', default => sub { [] });
has reauthTokens => (is => 'ro', lazy => 1, default => sub {
    my $ott = $_[0]->p->loadModule('Lemonldap::NG::Portal::Lib::OneTimeToken');
    $ott->timeout($_[0]->conf->{issuersTimeout});
    return $ott;
});

sub init {
    my ($self) = @_;
    return 0 unless $self->SUPER::init;
    if ($self->conf->{customPluginsParams}->{anasAccountClients}) {
        my $policies = eval {
            open my $fh, '<', '/opt/anas/account-policies.json' or die 'account policies unavailable';
            local $/;
            from_json(<$fh>);
        };
        unless (!$@ && ref($policies) eq 'ARRAY') {
            $self->logger->error('ANAS account policies unavailable');
            return 0;
        }
        $self->accountPolicies($policies);
        $self->addUnauthRoute('anas-account' => 'selfAccount', ['GET']);
        $self->addAuthRoute('anas-account' => 'selfAccount', ['GET']);
    }
    return 1;
}

sub checkDirectory { $_[0]->directory->refresh($_[1]) }

sub menuRuleExists {
    my ($self, $node, $policy) = @_;
    return 0 unless ref($node) eq 'HASH';
    if (($node->{type} // '') eq 'application') {
        return ($node->{options}->{uri} // '') eq $policy->{uri}
            && ($node->{options}->{display} // '') eq $policy->{rule};
    }
    for my $child (values %$node) {
        return 1 if ref($child) eq 'HASH' && $self->menuRuleExists($child, $policy);
    }
    return 0;
}

sub currentPolicies {
    my ($self, $req, $profile) = @_;
    my @policies;
    for my $policy (@{$self->accountPolicies}) {
        next unless ref($policy) eq 'HASH' && ref($policy->{groups}) eq 'ARRAY';
        my $type = $policy->{protocol} // '';
        my $valid = 0;
        if ($type eq 'oidc' || $type eq 'saml') {
            my $section = $type eq 'oidc' ? 'oidcRPMetaDataOptions' : 'samlSPMetaDataOptions';
            $valid = ($self->conf->{$section}->{$policy->{rp}}->{$section . 'Rule'} // '') eq $policy->{rule};
        } elsif ($type eq 'catalog') {
            $valid = $self->menuRuleExists($self->conf->{applicationList}, $policy);
        }
        next unless $valid;
        my $rule = $self->p->buildRule($policy->{rule}, 'ANAS account application');
        next unless $rule;
        push @policies, {name => $policy->{name}, groups => $policy->{groups},
            allowed => $rule->($req, $profile) ? JSON::true : JSON::false};
    }
    return \@policies;
}

# Restricted self-account API. A portal access token identifies its own subject;
# it cannot select another user or inspect sessions, credentials, or admin data.
sub selfAccount {
    my ($self, $req) = @_;
    my $header = $req->env->{HTTP_AUTHORIZATION} // '';
    my ($token) = $header =~ /^Bearer ([^\s]+)$/i;
    my $access = $token && $self->oidc->getAccessToken($token);
    my %clients = map { $_ => 1 } split /,/, ($self->conf->{customPluginsParams}->{anasAccountClients} // '');
    return $self->sendError($req, 'Access denied', 401)
        unless $access && $clients{$access->data->{client_id} // ''};
    my $rp = $self->oidc->getRP($access->data->{client_id});
    my $session = $self->oidc->_getSessionFromAccessTokenData($access->data);
    return $self->sendError($req, 'Access denied', 401) unless $rp && $session;
    my $anchor = $session->data->{anasIdentityAnchor};
    my $profile = eval { $self->directory->profile($anchor) };
    return $self->sendError($req, 'Directory unavailable', 503) if $@;
    return $self->sendError($req, 'Access denied', 403)
        unless $self->directory->enabled($profile);
    my $rule = $self->p->buildRule($self->conf->{oidcRPMetaDataOptions}->{$rp}->{oidcRPMetaDataOptionsRule}, 'ANAS account admission');
    return $self->sendError($req, 'Access denied', 403) unless $rule && $rule->($req, $profile);
    push @{$req->respHeaders}, 'Cache-Control' => 'no-store';
    return $self->sendJSONresponse($req, {
        schema => 'llng-account/v1', subject => $anchor, name => $profile->{sAMAccountName},
        enabled => JSON::true, groups => [sort keys %{$profile->{hGroups}}],
        applications => $self->currentPolicies($req, $profile),
    });
}

sub authenticatedNow {
    my ($self, $req) = @_;
    $req->data->{anasAuthenticatedNow} = 1;
    return PE_OK;
}

sub requestDigest {
    my ($self, $request) = @_;
    my %fields = map { $_ => ($request->{$_} // '') }
        qw(client_id redirect_uri response_type response_mode scope state nonce code_challenge code_challenge_method prompt);
    return sha256_hex(JSON->new->canonical->utf8->encode(\%fields));
}

sub takePdata {
    my ($self, $req, $name) = @_;
    if (ref($req->pdata->{keepPdata}) eq 'ARRAY') {
        @{$req->pdata->{keepPdata}} = grep { $_ ne $name } @{$req->pdata->{keepPdata}};
        delete $req->pdata->{keepPdata} unless @{$req->pdata->{keepPdata}};
    }
    return delete $req->pdata->{$name};
}

# Native renewsession redirects after authentication. Keep a single-use proof
# in native server-side OTT storage across that redirect, bound to the exact
# authorization request, subject and newly authenticated session.
sub recordAuthentication {
    my ($self, $req) = @_;
    return PE_OK unless $req->data->{anasAuthenticatedNow};
    my $id = $self->takePdata($req, 'anasReauthChallenge');
    return PE_OK unless $id;
    my $challenge = $self->reauthTokens->getToken($id);
    return PE_OK unless $challenge && $req->id
        && ($challenge->{subject} // '') eq ($req->sessionInfo->{anasIdentityAnchor} // '');
    $req->pdata->{anasReauthProof} = $self->reauthTokens->createToken({
        digest => $challenge->{digest}, subject => $challenge->{subject}, session => $req->id,
    });
    push @{$req->pdata->{keepPdata}}, 'anasReauthProof';
    return PE_OK;
}

sub checkAuthorization {
    my ($self, $req, $request) = @_;
    my $client = $request->{client_id} // '';
    my $rp = $self->oidc->getRP($client);
    return PE_OK unless $rp; # Let the issuer report an unknown client.
    if ($self->oidc->rpOptions->{$rp}->{oidcRPMetaDataOptionsPublic}) {
        return PE_ERROR unless ($request->{code_challenge_method} // '') eq 'S256'
            && ($request->{code_challenge} // '') =~ /^[A-Za-z0-9_-]{43}$/;
    }
    # Native anonymous prompt=none validation also evaluates the RP's group
    # rule, which requires a directory identity. Return only the native error
    # after validating its destination; no code or session is issued here.
    unless ($req->id) {
        if (($request->{prompt} // '') eq 'none') {
            return PE_ERROR unless ($request->{response_type} // '') eq 'code'
                && ($request->{scope} // '') =~ /(?:^|\s)openid(?:\s|$)/
                && ($request->{scope} // '') !~ /[^\x20\x21\x23-\x5B\x5D-\x7E]/
                && $self->oidc->_validateRedirectUri($req, $rp,
                    $request->{redirect_uri} // '', 'authorization');
            return $self->oidc->returnRedirectError($req,
                $request->{redirect_uri}, 'login_required', '', undef,
                $request->{state}, 0);
        }
        return PE_OK;
    }
    my %reauth = map { $_ => 1 } split /,/, ($self->conf->{customPluginsParams}->{anasReauthClients} // '');
    my $fresh = $req->data->{anasAuthenticatedNow};
    if ($reauth{$client}) {
        my $proofID = $self->takePdata($req, 'anasReauthProof');
        my $proof = $proofID && $self->reauthTokens->getToken($proofID);
        $fresh ||= $proof && ($proof->{digest} // '') eq $self->requestDigest($request)
            && ($proof->{subject} // '') eq ($req->sessionInfo->{anasIdentityAnchor} // '')
            && ($proof->{session} // '') eq ($req->id // '');
    }
    if ($reauth{$client} && !$fresh) {
        # LLNG 2.23.4 ignores max_age=0 and tolerates a recently authenticated
        # cookie for prompt=login. Sensitive portal operations require an actual
        # new password/Passkey interaction, regardless of cookie age.
        $req->pdata->{anasReauthChallenge} = $self->reauthTokens->createToken({
            digest => $self->requestDigest($request), subject => $req->sessionInfo->{anasIdentityAnchor},
        });
        push @{$req->pdata->{keepPdata}}, 'anasReauthChallenge';
        return $self->oidc->reAuth($req);
    }
    # The credential just verified in this request satisfies prompt=login.
    # Letting the issuer evaluate it again could loop on a slow directory call.
    delete $request->{prompt} if $reauth{$client};
    return $self->checkDirectory($req);
}

1;
