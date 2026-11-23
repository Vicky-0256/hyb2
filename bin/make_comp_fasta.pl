#!/usr/bin/perl
use strict;
use File::Temp qw(tempfile);
use IO::Handle;

my (%count, %barcode_count, %seen);
my $barcoded;
for my $path (@ARGV ? @ARGV : '-') {
    my $reads;
    if ($path eq '-') {
        $reads = \*STDIN;
    } else {
        open $reads, '<', $path or die "Cannot open $path: $!\n";
    }
    while (my $row = <$reads>) {
        chomp $row;
        $row =~ s/\r\z//;
        my ($name, $seq) = split /\t/, $row, 2;
        length($name) && defined($seq) && $seq =~ /\A\S+\z/
            or die "Cannot collapse read in $path: expected a name and a nonempty sequence without whitespace\n";
        $barcoded = ($row =~ /^[^\t]*_[A-Z]*\t/) unless defined $barcoded;
        $count{$seq}++;
        if ($barcoded) {
            my $barcode = (split /_/, $name, -1)[-1];
            $barcode_count{$seq}++ unless $seen{$seq, $barcode}++;
        }
    }
    $reads->error and die "Cannot read $path: $!\n";
}

# Flush all counts before sort opens the file. File::Temp exclusively creates
# it and removes it on exit, including errors, without assuming $$.tmp is ours.
my ($counts, $filename) = tempfile('hyb2-counts-XXXXXXXX', UNLINK => 1);
for my $seq (keys %count) {
    print {$counts} "$seq\t$count{$seq}", ($barcoded ? "\t$barcode_count{$seq}" : ''), "\n"
        or die "Cannot write $filename: $!\n";
}
close $counts or die "Cannot close $filename: $!\n";

my @keys = ('-k2,2nr');
push @keys, '-k3,3nr' if $barcoded;
open my $sorted, '-|', 'sort', @keys, $filename or die "Cannot run sort: $!\n";
my $rank = 0;
while (my $row = <$sorted>) {
    my ($seq, $total, $unique) = split ' ', $row;
    my $id = ++$rank . ($barcoded ? "-$unique" : '') . "_$total";
    print ">$id\n$seq\n" or die "Cannot write collapsed FASTA: $!\n";
}
$sorted->error and die "Cannot read sorted counts: $!\n";
close $sorted or die "sort failed while ranking read counts\n";
close STDOUT or die "Cannot finish writing collapsed FASTA: $!\n";
