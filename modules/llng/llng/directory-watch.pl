#!/usr/bin/perl
use strict;
use warnings;
use FindBin;
use lib $FindBin::Bin;
use JSON qw(decode_json);
use Fcntl qw(:flock);
use Lemonldap::NG::Portal;
require DirectoryWatch;
require DirectoryRevoke;

umask 0077;
my $state = $ENV{LLNG_DIRWATCH_STATE_DIR} // '/var/lib/anas-dirwatch';
if (@ARGV && $ARGV[0] eq '--healthcheck') {
    open my $fh, '<', "$state/health.json" or exit 1;
    local $/;
    my $health = eval {decode_json(<$fh>)};
    exit($health && !$health->{pending} && time - $health->{checked} <= 360 ? 0 : 1);
}
die "directory watcher must run without root\n" unless $>;
open my $lock, '>>', "$state/worker.lock" or die "private watcher state unavailable\n";
flock($lock, LOCK_EX | LOCK_NB) or die "directory watcher already running\n";
my $p = Lemonldap::NG::Portal->new;
$p->init({configStorage => {type => 'File', dirName => '/var/lib/lemonldap-ng/conf',
        confFile => '/opt/anas/runtime-seed/etc-lemonldap-ng/lemonldap-ng.ini'},
    logLevel => 'error', localSessionStorage => '', handlerInternalCache => 0})
    or die "native LLNG initialization failed\n";
my $journal = Anas::LLNG::DirectoryJournal->new(
    path => $ENV{LLNG_DIRWATCH_EVENT_FILE} // '/var/lib/anas-directory-events/events.jsonl',
    attributes => {map {lc($_) => 1} (qw(member memberOf userAccountControl lockoutTime accountExpires
        sAMAccountName userPrincipalName displayName mail anasIdentityAnchor),
        values %{$p->conf->{ldapExportedVars} // {}})});
my $watcher = Anas::LLNG::DirectoryWatch->new(state => $state, journal => $journal,
    adapter => Anas::LLNG::DirectoryRevoke->new(portal => $p, admin_group => $ENV{SAMBA_DC_ADMIN_GROUP_NAME}),
    log => sub {print STDERR $_[0], "\n"});
my $stop;
$SIG{TERM} = $SIG{INT} = sub {$stop = 1};
while (!$stop) {
    eval {
        if (-f '/var/lib/lemonldap-ng/conf/.anas-ready') {
            # Reuse native configuration reload, including issuer keys and
            # admission rules changed by Manager or the module reconciler.
            $p->HANDLER->checkConf() or die "configuration unavailable\n";
            $watcher->{adapter} = Anas::LLNG::DirectoryRevoke->new(
                portal => $p, admin_group => $ENV{SAMBA_DC_ADMIN_GROUP_NAME})
                if $watcher->{adapter}->{directory}->conf != $p->conf;
            $watcher->tick(time);
        } elsif (@ARGV && $ARGV[0] eq '--once') { die "configuration not ready\n"; }
    };
    # Do not log raw directory, DB, HTTP or token data from exception messages.
    if ($@) {
        print STDERR "directory reconcile failed; cursor retained\n";
        exit 1 if @ARGV && $ARGV[0] eq '--once';
    }
    last if @ARGV && $ARGV[0] eq '--once';
    sleep 1;
}
