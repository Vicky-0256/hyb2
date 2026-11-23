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
