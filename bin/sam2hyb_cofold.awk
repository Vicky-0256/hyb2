# Write one RNAcofold FASTA record for each forward/forward HYB row.
# The two inputs are the original reference FASTA and a tab-separated HYB file.
BEGIN { FS = "\t" }

function fail(message) {
    failed = 1
    print "sam2hyb: " FILENAME ":" FNR ": " message > "/dev/stderr"
    exit 1
}

FILENAME == ARGV[1] {
    sub(/\r$/, "")
    if (/^[[:space:]]*$/ || /^[#;]/) next
    if (/^>/) {
        if (length(name) && !length(reference["@" name])) fail("empty reference sequence for " name)
        name = substr($0, 2)
        sub(/^[[:space:]]+/, "", name)
        sub(/[[:space:]].*$/, "", name)
        if (!length(name) || ("@" name) in reference) fail("empty or duplicate FASTA identifier: " name)
        reference["@" name] = ""
    } else {
        gsub(/[[:space:]]/, "")
        if (!length(name) || $0 !~ /^[[:alpha:]]+$/) fail("invalid FASTA sequence")
        reference["@" name] = reference["@" name] toupper($0)
    }
    next
}

function arm(id, start, end) {
    if (!(("@" id) in reference)) fail("reference FASTA has no entry for " id)
    if (end > length(reference["@" id])) fail("HYB coordinates exceed the reference length for " id)
    return substr(reference["@" id], start, end - start + 1)
}

!/^#/ && NF {
    if (NF < 15) fail("expected at least 15 tab-separated HYB columns")
    if ($7 !~ /^[0-9]+$/ || $8 !~ /^[0-9]+$/ || $13 !~ /^[0-9]+$/ || $14 !~ /^[0-9]+$/ ||
        $7 < 1 || $8 < 1 || $13 < 1 || $14 < 1) fail("invalid HYB coordinates")
    if ($7 > $8 || $13 > $14) next
    first = arm($4, $7, $8)
    second = arm($10, $13, $14)
    print ">hyb2-row-" FNR
    print first "&" second
}

END {
    if (!failed && (!length(name) || !length(reference["@" name]))) {
        print "sam2hyb: missing sequence in reference FASTA" > "/dev/stderr"
        exit 1
    }
}
