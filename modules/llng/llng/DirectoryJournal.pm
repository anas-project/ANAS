package Anas::LLNG::DirectoryJournal;

use strict;
use warnings;
use JSON qw(decode_json encode_json);
use Fcntl qw(:DEFAULT :flock);
use File::Basename qw(dirname);
use File::Temp qw(tempfile);
use IO::Handle;

sub new {
    my ($class, %args) = @_;
    return bless { %args, cache => {} }, $class;
}

# The producer retains one rotated generation. Read complete records only;
# inode/size/mtime caching keeps an idle subscriber from reparsing the journal.
sub snapshot {
    my ($self, $cursor) = @_;
    my %records;
    my $found;
    for my $path ($self->{path} . '.1', $self->{path}) {
        my @stat = stat($path);
        next if !@stat && $!{ENOENT};
        die "journal unavailable\n" unless @stat;
        $found = 1;
        my $stamp = join(':', @stat[0, 1, 7, 9, 10]);
        my $cached = $self->{cache}->{$path};
        unless ($cached && $cached->{stamp} eq $stamp) {
            open my $fh, '<:raw', $path or die "journal read failed\n";
            my @rows;
            while (my $line = <$fh>) {
                last unless $line =~ /\n\z/;
                next if $line =~ /^\s*$/;
                my $row = eval { decode_json($line) };
                die "invalid complete journal record\n" if $@ || ref($row) ne 'HASH'
                    || !defined($row->{seq}) || $row->{seq} !~ /^[1-9][0-9]*$/
                    || ($row->{op} // '') !~ /^(Add|Modify|Delete)$/
                    || ref($row->{attributes}) ne 'ARRAY' || !length($row->{dn} // '');
                push @rows, $row;
            }
            close $fh or die "journal close failed\n";
            $cached = $self->{cache}->{$path} = {stamp => $stamp, rows => \@rows};
        }
        for my $row (@{$cached->{rows}}) {
            my $old = $records{$row->{seq}};
            die "conflicting journal sequence\n" if $old
                && JSON->new->canonical->encode($old) ne JSON->new->canonical->encode($row);
            $records{$row->{seq}} = $row;
        }
    }
    die "previous journal unavailable\n" if !$found && $cursor;
    my @seq = sort {$a <=> $b} keys %records;
    my $last = @seq ? $seq[-1] : 0;
    my $reset = $last < $cursor;
    my @new = grep { $reset || $_ > $cursor } @seq;
    my $gap = $reset;
    my $expected = $reset ? 1 : $cursor + 1;
    for my $seq (@new) {
        $gap = 1 if $seq != $expected;
        $expected = $seq + 1;
    }
    return {cursor => $last, gap => $gap ? 1 : 0, rows => [map {$records{$_}} @new]};
}

sub relevant {
    my ($self, $row) = @_;
    return 1 if $row->{op} eq 'Add' || $row->{op} eq 'Delete';
    return scalar grep { $self->{attributes}->{lc $_} } @{$row->{attributes}};
}

# State contains opaque session references and must be private. fsync both the
# replacement and its parent before acknowledging events or deleting grants.
sub save {
    my ($class, $path, $value) = @_;
    my $dir = dirname($path);
    my ($fh, $temp) = tempfile('.state-XXXXXXXX', DIR => $dir, UNLINK => 0);
    eval {
        chmod 0600, $temp or die "state permissions failed\n";
        print {$fh} encode_json($value) or die "state write failed\n";
        $fh->flush && $fh->sync or die "state sync failed\n";
        close $fh or die "state close failed\n";
        rename $temp, $path or die "state replace failed\n";
        sysopen my $directory, $dir, O_RDONLY or die "state directory unavailable\n";
        $directory->sync or die "state directory sync failed\n";
        close $directory;
    };
    if ($@) { unlink $temp; die $@; }
}

sub load {
    my ($class, $path) = @_;
    return {cursor => 0, pending => {}, reconciled => 0} unless -e $path;
    open my $fh, '<', $path or die "state unavailable\n";
    local $/;
    my $state = eval { decode_json(<$fh>) };
    close $fh;
    unless (!$@ && ref($state) eq 'HASH' && ($state->{cursor} // '') =~ /^[0-9]+$/
        && ref($state->{pending}) eq 'HASH' && ($state->{reconciled} // '') =~ /^[0-9]+$/
        && (!exists($state->{limitations}) || ref($state->{limitations}) eq 'HASH')) {
        # Never erase potentially undelivered logout notifications. A corrupt
        # state requires recovery from backup instead of silently resetting it.
        die "invalid durable directory state; restore its last valid backup\n";
    }
    return $state;
}

1;
