use strict;
use warnings;
use Test::More;
use Lemonldap::NG::Portal::Plugins::AnasOIDC;
use Lemonldap::NG::Portal::Main;
use Lemonldap::NG::Portal::Main::Constants qw(PE_OK PE_ERROR PE_RENEWSESSION PE_REDIRECT);

{ package TestIssuer;
  sub getRP { $_[1] eq 'unknown' ? undef : $_[1] }
  sub rpOptions { { public => {oidcRPMetaDataOptionsPublic => 1}, reauth => {oidcRPMetaDataOptionsPublic => 1}, confidential => {} } }
  sub reAuth { Lemonldap::NG::Portal::Main::Constants::PE_RENEWSESSION() }
  sub _validateRedirectUri { $_[3] eq 'https://portal.example/callback' }
  sub returnRedirectError { $_[1]->{native_error} = {uri => $_[2], error => $_[3], state => $_[6]}; Lemonldap::NG::Portal::Main::Constants::PE_REDIRECT() }
}
{ package TestRequest;
  sub data { $_[0]->{data} ||= {} }
  sub pdata { $_[0]->{pdata} ||= {} }
  sub sessionInfo { {anasIdentityAnchor => 'fixture-identity'} }
  sub id { $_[0]->{anonymous} ? undef : ($_[0]->{id} // 'old-session') }
}
{ package TestCleaner;
  sub conf { {whatToTrace => 'anasIdentityAnchor'} }
  sub logger { bless {}, 'TestLogger' }
  sub auditLog { }
}
{ package TestLogger; sub debug {} }
{ package TestTokens;
  sub createToken { my ($self, $data) = @_; my $id = ++$self->{next}; $self->{$id} = $data; return $id; }
  sub getToken { delete $_[0]->{$_[1]} }
}
{ package TestDirectory;
  sub refresh { $_[0]->{denied} ? Lemonldap::NG::Portal::Main::Constants::PE_ERROR() : Lemonldap::NG::Portal::Main::Constants::PE_OK() }
}

my $conf = { customPluginsParams => { anasReauthClients => 'reauth' } };
my $directory = bless {}, 'TestDirectory';
my $plugin = Lemonldap::NG::Portal::Plugins::AnasOIDC->new(conf => $conf, oidc => bless({}, 'TestIssuer'), directory => $directory, reauthTokens => bless({}, 'TestTokens'));
my $req = bless {}, 'TestRequest';
my %valid = (client_id => 'public', code_challenge_method => 'S256', code_challenge => ('A' x 43));
my $anonymous = bless {anonymous => 1}, 'TestRequest';
$directory->{denied} = 1;
my %silent = (%valid, prompt => 'none', response_type => 'code', scope => 'openid profile email', redirect_uri => 'https://portal.example/callback', state => 'bound-state');
is($plugin->checkAuthorization($anonymous, {%silent}), PE_REDIRECT, 'anonymous silent request returns native login_required');
is_deeply($anonymous->{native_error}, {uri => 'https://portal.example/callback', error => 'login_required', state => 'bound-state'}, 'silent error preserves registered callback and state');
for my $invalid ({redirect_uri => 'https://foreign.example/callback'}, {response_type => 'token'}, {scope => 'profile'}) {
    is($plugin->checkAuthorization($anonymous, {%silent, %$invalid}), PE_ERROR, 'invalid anonymous request cannot redirect');
}
is($plugin->checkAuthorization($anonymous, {%valid, client_id => 'reauth', prompt => 'login'}), PE_OK, 'anonymous reauthentication reaches native login');
is($plugin->checkAuthorization($anonymous, {%valid, code_challenge_method => 'plain'}), PE_ERROR, 'anonymous public client still requires S256');
$directory->{denied} = 0;
is($plugin->checkAuthorization($req, {%valid}), PE_OK, 'public S256 accepted');
for my $method ('plain', '', 's256') {
    is($plugin->checkAuthorization($req, {%valid, code_challenge_method => $method}), PE_ERROR, 'public downgrade rejected');
}
is($plugin->checkAuthorization($req, {%valid, code_challenge => 'short'}), PE_ERROR, 'invalid SHA256 challenge rejected');
is($plugin->checkAuthorization($req, {client_id => 'confidential'}), PE_OK, 'confidential native flow preserved');
is($plugin->checkAuthorization($req, {client_id => 'unknown'}), PE_OK, 'unknown client left to native validator');
my $reauth = {%valid, client_id => 'reauth', prompt => 'login'};
is($plugin->checkAuthorization($req, $reauth), PE_RENEWSESSION, 'SSO cookie cannot satisfy fresh authentication');
is($plugin->authenticatedNow($req), PE_OK, 'actual authentication marked');
is($plugin->checkAuthorization($req, $reauth), PE_OK, 'fresh credentials satisfy reauthentication');
ok(!exists($reauth->{prompt}), 'do not loop after a slow successful authentication');
my $next = bless {}, 'TestRequest';
is($plugin->checkAuthorization($next, {%valid, client_id => 'reauth'}), PE_RENEWSESSION, 'freshness does not leak to another request');
$directory->{denied} = 1;
is($plugin->checkAuthorization($req, {%valid}), PE_ERROR, 'old SSO cookie cannot bypass current directory status');
is($plugin->checkDirectory($req), PE_ERROR, 'SAML uses the same current directory check');
$directory->{denied} = 0;
my $request = {%valid, client_id => 'reauth', state => 'one', prompt => 'login'};
my $initial = bless {}, 'TestRequest';
is($plugin->checkAuthorization($initial, {%$request}), PE_RENEWSESSION, 'start redirect-bound interaction');
my $login = bless {pdata => $initial->pdata, id => 'new-session'}, 'TestRequest';
$plugin->authenticatedNow($login);
is($plugin->recordAuthentication($login), PE_OK, 'native successful authentication creates one-time proof');
my $proof = $login->pdata->{anasReauthProof};
Lemonldap::NG::Portal::Main::cleanPdata(bless({}, 'TestCleaner'), $login);
is($login->pdata->{anasReauthProof}, $proof, 'native endAuth cleanup preserves the redirect proof');
my $callback = bless {pdata => $login->pdata, id => 'new-session'}, 'TestRequest';
is($plugin->checkAuthorization($callback, {%$request}), PE_OK, 'proof survives native redirect without another authentication');
ok(!exists($callback->pdata->{keepPdata}), 'completed proof leaves no persistent cookie field');
$callback->pdata->{anasReauthProof} = $proof;
is($plugin->checkAuthorization($callback, {%$request}), PE_RENEWSESSION, 'consumed proof cannot be replayed');
for my $wrong ('state', 'session', 'subject') {
    my $value = {digest => $plugin->requestDigest($request), session => 'new-session', subject => 'fixture-identity'};
    $value->{digest} = 'wrong' if $wrong eq 'state';
    $value->{$wrong} = 'wrong' if $wrong ne 'state';
    my $other = bless {id => 'new-session', pdata => {anasReauthProof => $plugin->reauthTokens->createToken($value)}}, 'TestRequest';
    is($plugin->checkAuthorization($other, {%$request}), PE_RENEWSESSION, 'proof cannot cross '.$wrong.' binding');
}
done_testing();
