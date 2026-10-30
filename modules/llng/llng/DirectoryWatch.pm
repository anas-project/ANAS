package Anas::LLNG::DirectoryWatch;

use strict;
use warnings;
use JSON qw(decode_json);
require DirectoryJournal;

sub new {
    my ($class, %args) = @_;
    my $state = Anas::LLNG::DirectoryJournal->load($args{state} . '/pending.json');
    my $cursor = {cursor => 0, reconciled => 0};
    if (open my $fh, '<', $args{state} . '/cursor.json') {
        local $/;
        my $saved = eval {decode_json(<$fh>)};
        $cursor = $saved if !$@ && ref($saved) eq 'HASH'
            && ($saved->{cursor} // '') =~ /^[0-9]+$/ && ($saved->{reconciled} // '') =~ /^[0-9]+$/;
        close $fh;
    }
    return bless {%args, pending => $state->{pending}, limitations => $state->{limitations} // {},
        %$cursor, last_attempt => 0}, $class;
}

sub savePending {
    my ($self) = @_;
    Anas::LLNG::DirectoryJournal->save($self->{state} . '/pending.json',
        {cursor => 0, reconciled => 0, pending => $self->{pending}, limitations => $self->{limitations}});
}

sub tick {
    my ($self, $now) = @_;
    my $batch = $self->{journal}->snapshot($self->{cursor});
    my $changed = $batch->{gap} || grep {$self->{journal}->relevant($_)} @{$batch->{rows}};
    $self->{first_event} //= $now if $changed;
    my $due = !$self->{reconciled} || $now - $self->{reconciled} >= 300
        || ($changed && $now - $self->{first_event} >= 2);
    my $retry = keys %{$self->{pending}};
    return if ($self->{last_attempt} && $now - $self->{last_attempt} < 5);
    return unless $due || $retry || $batch->{cursor} != $self->{cursor};
    $self->{last_attempt} = $now;
    if ($due) {
        # Reconcile even when an earlier application's callback is failing so
        # that another account's disable event is not blocked behind it.
        my $plan = $self->{adapter}->plan;
        for my $key (keys %$plan) {
            # Preserve failed notifications across directory re-enablement.
            $self->{pending}->{$key} = $plan->{$key};
        }
        $self->savePending;
    }
    # Persisting the plan precedes all mutations. Revoke native grants first;
    # callbacks can then be retried without leaving access/refresh tokens live.
    my @keys = sort {
        ($self->{pending}->{$a}->{type} eq 'notify') <=> ($self->{pending}->{$b}->{type} eq 'notify') || $a cmp $b
    } keys %{$self->{pending}};
    my $failed = 0;
    for my $key (@keys) {
        my $result;
        my $ok = eval {$result = $self->{adapter}->apply($self->{pending}->{$key}); 1};
        if ($ok) {
            if (ref($result) eq 'HASH' && $result->{limitation}) {
                $self->{limitations}->{$result->{limitation}} = {reason => 'no-backchannel', last_seen => $now};
            }
            delete $self->{pending}->{$key};
        }
        else { $failed++; }
    }
    $self->savePending;
    if (!$failed && !keys %{$self->{pending}} && ($due || !$changed)) {
        $self->{cursor} = $batch->{cursor};
        $self->{reconciled} = $now if $due;
        delete $self->{first_event};
        Anas::LLNG::DirectoryJournal->save($self->{state} . '/cursor.json',
            {cursor => $self->{cursor}, reconciled => $self->{reconciled}});
        $self->{log}->('directory reconcile committed') if $due;
    } elsif ($failed) { $self->{log}->('directory revoke pending; retry scheduled'); }
    Anas::LLNG::DirectoryJournal->save($self->{state} . '/health.json',
        {checked => $now, pending => scalar(keys %{$self->{pending}}), cursor => $self->{cursor},
            limited_clients => [sort keys %{$self->{limitations}}]});
}

1;
