use strict;
use warnings;
use Test::More;
use Lemonldap::NG::Portal::Plugins::AnasDirectory;

my $directory = Lemonldap::NG::Portal::Plugins::AnasDirectory->new;
my $normal = {anasAccountControl => 512, anasAccountState => 0, anasAccountExpires => 0};
ok($directory->enabled($normal, 100), 'normal account accepted');
ok($directory->enabled({%$normal, anasAccountState => 8388608}, 100), 'password age does not disable Passkey');
for my $change ({anasAccountControl => 514}, {anasAccountState => 16}, {anasAccountExpires => 1}, {anasAccountControl => undef}, {anasAccountState => 'unknown'}) {
    ok(!$directory->enabled({%$normal, %$change}, 100), 'disabled, locked, expired or unknown account denied');
}
ok(!$directory->enabled(undef), 'deleted account denied');
ok(!$directory->enabled({%$normal, anasAccountExpires => 11644473700 * 10000000}, 100), 'exact expiry boundary denied');
ok($directory->enabled({%$normal, anasAccountExpires => 11644473701 * 10000000}, 100), 'future expiry accepted');
for my $anchor ('', '*)(objectClass=*)', 'username') {
    eval { $directory->profile($anchor) };
    like($@, qr/invalid directory identity/, 'malformed identity rejected before LDAP');
}
done_testing();
