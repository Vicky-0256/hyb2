#!/usr/bin/perl
# Add RNAcofold MFE to column 3, preserving the remaining HYB columns.
# The optional input FASTA checks every backend result against the requested row.
use strict;
use warnings;

@ARGV == 2 || @ARGV == 3 or die "Usage: $0 input.hyb output.vienna [cofold-input.fa]\n";
my ($hyb_path, $vienna_path, $pairs_path) = @ARGV;
open my $hyb, '<', $hyb_path or die "Cannot open $hyb_path: $!\n";
open my $vienna, '<', $vienna_path or die "Cannot open $vienna_path: $!\n";

sub next_line {
    my ($fh) = @_;
    my $line = <$fh>;
    $line =~ s/\r?\n\z// if defined $line;
    return $line;
}

sub next_result {
    my $header;
    do { $header = next_line($vienna) } while defined($header) && $header =~ /^\s*$/;
    return unless defined $header;
    $header =~ s/^>//;
    $header =~ s/&>/-/g;  # paired headers in legacy .vienna files
    my $sequence = next_line($vienna);
    my $fold = next_line($vienna);
    my $number = qr/[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][+-]?\d+)?/;
    defined($fold) && $sequence =~ /^[A-Za-z]+(?:&[A-Za-z]+)?$/ &&
        $fold =~ /^\s*([().]+(?:&[().]+)?)\s+\(\s*($number)\s*\)\s*$/
        or die "Malformed or truncated RNAcofold result for '$header'\n";
    my ($structure, $energy) = ($1, sprintf('%.2f', $2));
    length($sequence) == length($structure) && index($sequence, '&') == index($structure, '&') &&
        $energy =~ /^-?\d+\.\d\d$/ or die "Invalid RNAcofold structure or energy for '$header'\n";
    my $depth = 0;
    while ($structure =~ /([()])/g) {
        $depth += $1 eq '(' ? 1 : -1;
        last if $depth < 0;
    }
    die "Unbalanced RNAcofold structure for '$header'\n" if $depth;
    return ($header, $sequence, $energy);
}

my ($pairs, $expected);
my (%energies, %legacy_read);
if (defined $pairs_path) {
    open $pairs, '<', $pairs_path or die "Cannot open $pairs_path: $!\n";
    $expected = next_line($pairs);
} else {
    # Historical output uses readID_ref_start_end-readID_ref_start_end.
    # A raw RNAcofold record may instead use the read ID as its entire header.
    while (my ($id, $seq, $energy) = next_result()) {
        $energies{$id} = $energy;
        # Early two-file exports used only the first two underscore-delimited
        # fields to join decorated headers; the last such record won.
        $legacy_read{$1} = $energy if $id =~ /^([^_]+_[^_]+)_/;
    }
}

my $row = 0;
while (my $line = <$hyb>) {
    ++$row;
    if ($line =~ /^\s*(?:#|$)/) { print $line; next }
    my $ending = $line =~ s/(\r?\n)\z// ? $1 : '';
    my @fields = split /\t/, $line, -1;
    @fields >= 3 or die "$hyb_path:$row: expected tab-separated HYB columns\n";
    my $energy;
    if (defined $pairs_path) {
        if (defined($expected) && $expected eq ">hyb2-row-$row") {
            my $input = next_line($pairs);
            my ($id, $seq, $dg) = next_result();
            defined($id) && defined($input) && ">$id" eq $expected && uc($seq) eq uc($input)
                or die "Missing or mismatched RNAcofold result for HYB row $row\n";
            $energy = $dg;
            $expected = next_line($pairs);
        }
    } else {
        $energy = $energies{$fields[0]};
        if (!defined($energy) && @fields >= 14) {
            my $left = join '_', @fields[0, 3, 6, 7];
            my $right = join '_', @fields[0, 9, 12, 13];
            $energy = $energies{"$left-$right"};
        }
        $energy = $legacy_read{$fields[0]} unless defined $energy;
    }
    $fields[2] = defined($energy) ? $energy : '.';
    print join("\t", @fields), $ending;
}
if (defined $pairs_path) {
    die "Unmatched HYB row in RNAcofold input\n" if defined $expected;
    my @extra = next_result();
    die "Unexpected extra RNAcofold result '$extra[0]'\n" if @extra;
}
