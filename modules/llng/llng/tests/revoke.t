use strict;
use warnings;
use Test::More;
use FindBin;
use lib "$FindBin::Bin/..";
require DirectoryRevoke;

{ package TestDirectory;
  sub profile { $_[0]->{profiles}->{$_[1]} }
  sub enabled { $_[1] && !$_[1]->{disabled} }
}
{ package TestPortal;
  sub conf { $_[0]->{conf} }
  sub loadedModules { {'Lemonldap::NG::Portal::Issuer::OpenIDConnect' => bless({}, 'TestOIDC')} }
  sub buildRule { my ($self, $group) = @_; return sub {$_[1]->{hGroups}->{$group}} }
  sub setMacros { }
}
{ package TestOIDC;
  sub getRP { $_[1] }
  sub getSidFromSession { $_[2]->{_oidc_sid} // $_[2]->{_session_storage_id} }
  our $callback_failure = 0;
  sub _backChannelLogout { $callback_failure }
}
{ package TestRequest;
  sub sessionInfo { $_[0]->{data} }
}
{ package TestRevoker;
  our @ISA = ('Anas::LLNG::DirectoryRevoke');
  sub request { bless {data => $_[1]}, 'TestRequest' }
  sub sessions { $_[0]->{stores}->{$_[1]} }
  sub session { $_[0]->{session} }
}
{ package TestSession;
  sub _tie_session { $_[0]->{data} }
}
my $old = {anasIdentityAnchor => 'fixture', _session_kind => 'SSO',
    sAMAccountName => 'person', hGroups => {A => 1, B => 1}, _oidcConnectedRPIDs => 'a,b'};
my $other = {%$old, anasIdentityAnchor => 'other'};
my $current = {%$old, hGroups => {B => 1}};
my $directory = bless {profiles => {fixture => $current, other => $other}}, 'TestDirectory';
my $portal = bless {conf => {oidcRPMetaDataOptions => {
    a => {oidcRPMetaDataOptionsRule => 'A', oidcRPMetaDataOptionsClientID => 'a'},
    b => {oidcRPMetaDataOptionsRule => 'B', oidcRPMetaDataOptionsClientID => 'b'}},
    oidcRPMetaDataExportedVars => {a => {username => 'sAMAccountName'}, b => {username => 'sAMAccountName'}}}}, 'TestPortal';
my $revoker = bless {portal => $portal, directory => $directory, admin_group => 'Admins',
    stores => {global => {s1 => $old, s2 => $other}, oidc => {
        ta => {client_id => 'a', user_session_id => 's1'},
        tb => {client_id => 'b', user_session_id => 's1'},
        tc => {client_id => 'a', user_session_id => 's2'}}}}, 'TestRevoker';
my $plan = $revoker->plan;
my @notifications = grep {$_->{type} eq 'notify'} values %$plan;
is(scalar @notifications, 1, 'removing application A does not log out B or another user');
is($notifications[0]->{client}, 'a', 'notification targets affected client');
ok($plan->{'oidc:ta'} && !$plan->{'oidc:tb'} && !$plan->{'oidc:tc'}, 'only affected client grants removed');
is_deeply($plan->{'global:s1'}->{remove_clients}, ['a'], 'remove only affected native registration');
$revoker->{session} = bless {data => {%$old, _oidcConnectedRPIDs => 'a,b,new-client'}}, 'TestSession';
$revoker->apply($plan->{'global:s1'});
is($revoker->{session}->{data}->{_oidcConnectedRPIDs}, 'b,new-client', 'application registered after planning survives directory update');
ok(!$plan->{'global:s2'}, 'unchanged user does not cause a session write');
$current->{disabled} = 1;
$plan = $revoker->plan;
is($plan->{'global:s1'}->{type}, 'remove', 'disabled user loses SSO');
ok($plan->{'oidc:ta'} && $plan->{'oidc:tb'} && !$plan->{'oidc:tc'}, 'disabled user loses all its grants only');
delete $directory->{profiles}->{fixture};
is($revoker->plan->{'global:s1'}->{type}, 'remove', 'deleted identity loses SSO');
$directory->{profiles}->{fixture} = {%$old, sAMAccountName => 'renamed'};
$plan = $revoker->plan;
is(scalar(grep {$_->{type} eq 'notify'} values %$plan), 2, 'rename refreshes applications that consume login name');
is($plan->{'global:s1'}->{fields}->{anasIdentityAnchor}, 'fixture', 'rename retains stable identity');
$portal->{conf}->{oidcRPMetaDataExportedVars}->{a}->{groups} = 'groups;string;always';
$old->{hGroups}->{Admins} = 1;
$directory->{profiles}->{fixture} = {%$old, hGroups => {A => 1, B => 1}};
$plan = $revoker->plan;
@notifications = grep {$_->{type} eq 'notify'} values %$plan;
is(scalar @notifications, 1, 'typed group claim triggers administrator-role revocation only for its consumer');
is($notifications[0]->{client}, 'a', 'unrelated consumer remains signed in after role removal');
my $notification = $notifications[0];
eval {$revoker->apply($notification)};
like($@, qr/no background logout capability/, 'unaccepted unsupported client blocks acknowledgement');
$portal->{conf}->{customPluginsParams}->{anasBackgroundLogoutExceptions} = 'a';
is_deeply($revoker->apply($notification), {limitation => 'a'}, 'explicit exception reports limitation rather than logout success');
eval {$revoker->apply({%$notification, client => 'missing'})};
like($@, qr/configuration changed/, 'exception cannot bypass changed client identity');
$portal->{conf}->{oidcRPMetaDataOptions}->{a}->{oidcRPMetaDataOptionsLogoutType} = 'back';
$portal->{conf}->{oidcRPMetaDataOptions}->{a}->{oidcRPMetaDataOptionsLogoutUrl} = 'https://fixture/logout';
$TestOIDC::callback_failure = 1;
eval {$revoker->apply($notification)};
like($@, qr/application logout pending/, 'accepted limitation never suppresses an actual callback failure');
$TestOIDC::callback_failure = 0;
ok(!defined($revoker->apply($notification)), 'upgraded client uses its real receiver even while listed as an exception');
done_testing;
