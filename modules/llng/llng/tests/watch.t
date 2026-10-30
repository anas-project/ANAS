use strict;
use warnings;
use Test::More;
use File::Temp qw(tempdir);
use JSON qw(encode_json decode_json);
use FindBin;
use lib "$FindBin::Bin/..";
require DirectoryWatch;

{ package TestAdapter;
  sub plan { $_[0]->{scans}++; return {remove => {type => 'remove'}, logout => {type => 'notify'}} }
  sub apply {
    my ($self, $action) = @_;
    # Deleting grants before the callback is durable would lose retries.
    my $state = Anas::LLNG::DirectoryJournal->load($self->{dir} . '/pending.json');
    die 'notification was not persisted' unless $state->{pending}->{logout};
    push @{$self->{applied}}, $action->{type};
    die 'temporary application outage' if $action->{type} eq 'notify' && $self->{fail};
    return {limitation => 'vikunja'} if $action->{type} eq 'notify' && $self->{limited};
    return;
  }
}
my $dir = tempdir(CLEANUP => 1);
my $path = "$dir/events.jsonl";
sub event {
    my ($seq) = @_;
    open my $fh, '>>', $path or die $!;
    print {$fh} encode_json({seq => $seq, op => 'Modify', dn => 'CN=test', attributes => ['member']}) . "\n";
    close $fh;
}
sub worker {
    my ($adapter) = @_;
    return Anas::LLNG::DirectoryWatch->new(state => $dir, adapter => $adapter, log => sub {},
        journal => Anas::LLNG::DirectoryJournal->new(path => $path, attributes => {member => 1}));
}
my $adapter = bless {dir => $dir, fail => 1, applied => []}, 'TestAdapter';
my $watch = worker($adapter);
event(1);
$watch->tick(100);
is($watch->{cursor}, 0, 'callback failure does not commit journal');
is_deeply($adapter->{applied}, ['remove', 'notify'], 'native grant removal precedes callback');
ok(-f "$dir/pending.json", 'failed callback retained durably');
my $restart = worker($adapter);
ok($restart->{pending}->{logout}, 'restart restores pending callback');
$adapter->{fail} = 0;
$restart->tick(101);
is($restart->{cursor}, 1, 'successful retry commits after restart');
is(scalar(keys %{$restart->{pending}}), 0, 'acknowledged callback cleared');
my $scans = $adapter->{scans};
event(2); $restart->tick(106);
event(3); $restart->tick(107);
event(4); $restart->tick(108);
is($adapter->{scans}, $scans, 'burst is debounced');
$restart->tick(111);
is($adapter->{scans}, $scans + 1, 'continuing burst cannot postpone indefinitely');
is($restart->{cursor}, 4, 'single successful reconcile covers burst');
$restart->tick(412);
is($adapter->{scans}, $scans + 2, 'periodic authoritative reconcile without events');
open my $bad, '>', "$dir/cursor.json"; print {$bad} '{broken'; close $bad;
my $recovered = worker($adapter);
is($recovered->{cursor}, 0, 'corrupt cursor triggers full reconciliation');
$recovered->tick(413);
is($recovered->{cursor}, 4, 'full reconciliation establishes a valid cursor');
$adapter->{limited} = 1;
event(5); $recovered->tick(418); $recovered->tick(423);
is($recovered->{cursor}, 5, 'accepted unsupported client does not block event acknowledgement');
my $limited = worker($adapter);
is_deeply($limited->{limitations}, {vikunja => {reason => 'no-backchannel', last_seen => 423}},
    'accepted limitation survives restart separately from pending failures');
is(scalar(keys %{$limited->{pending}}), 0, 'accepted limitation does not remain a failed callback');
open my $health, '<', "$dir/health.json"; my $h = do {local $/; decode_json(<$health>)}; close $health;
is_deeply($h->{limited_clients}, ['vikunja'], 'health exposes the accepted client limitation');
done_testing;
