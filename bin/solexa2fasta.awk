#!/usr/bin/awk -f

function invalid(reason) {
	print "Invalid FASTQ at line " NR ": " reason > "/dev/stderr"
	exit 1
}

{
	sub(/\r$/, "")
	if ($0 !~ /^@[^[:space:]]/) invalid("expected a named read header")
	sub(/^@/, ">@", $1)
	header = $0
	if ((getline seq) != 1 || (getline plus) != 1 || (getline qual) != 1) invalid("incomplete record")
	sub(/\r$/, "", seq)
	sub(/\r$/, "", qual)
	if (plus !~ /^\+/) invalid("expected a + separator")
	if (!length(seq) || length(seq) != length(qual)) invalid("sequence and quality must have equal, nonzero lengths")
	if (toupper(seq) !~ /^[ACGTURYSWKMBDHVN]+$/) invalid("expected unspaced nucleotide sequence")
	if (qual ~ /[^!-~]/) invalid("expected printable quality scores")
	print header
	print seq
}

END { if (!NR) invalid("empty input") }
