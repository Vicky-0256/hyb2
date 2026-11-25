# Validate and copy unaligned nucleotide FASTA without loading whole records.
# Bowtie2 can silently skip empty records or discard non-base characters.
function invalid(reason) {
    print "Invalid FASTA at line " NR ": " reason > "/dev/stderr"
    failed = 1
    exit 1
}
{ sub(/\r$/, "") }
/^[#;]/ || /^[[:space:]]*$/ { next }
/^>/ {
    if (records && !bases) invalid("record has no sequence")
    sub(/^>[[:space:]]*/, "")
    if (!length($0)) invalid("missing record identifier")
    # reads=1: SAM read names cannot start with @ (solexa2fasta.awk writes >@name),
    # and every SAM reader would take such an alignment for a header line.
    if (reads && sub(/^@+/, "")) {
        if (!length($0) || $0 ~ /^[[:space:]]/) invalid("missing read name after @")
        if (!renamed++) print "warning: read names start with '@' (as in >@HWUSI-...); the leading '@' is removed, as SAM read names cannot start with it" > "/dev/stderr"
    }
    print ">" $0
    records++; bases = 0
    next
}
{
    if (!records) invalid("expected a record header")
    if (toupper($0) !~ /^[ACGTURYSWKMBDHVN]+$/) invalid("expected unspaced nucleotide sequence")
    print
    bases = 1
}
END {
    if (!failed && (!records || !bases)) invalid("expected a nonempty record")
}
