#!/usr/bin/awk -f
# Select the exact FASTA identifier (first header word) and read all its lines.
# -v id=NAME with length_only=1 gives its length; start/count select a window.
/^>/ {
	sub(/^>[[:space:]]*/, "")
	selected = (("@" $1) == ("@" id))
	if (selected) found++
	next
}
selected && !/^[#;]/ {
	gsub(/[[:space:]]/, "")
	sequence = sequence $0
}
END {
	if (found != 1 || !length(sequence)) {
		print "Expected one nonempty FASTA record for " id " in " FILENAME > "/dev/stderr"
		exit 1
	}
	if (length_only) { print length(sequence); exit }
	if (start < 1 || count < 1 || start+count-1 > length(sequence)) {
		print "Requested window exceeds " id " (" length(sequence) " bases) in " FILENAME > "/dev/stderr"
		exit 1
	}
	print substr(sequence, start, count)
}
