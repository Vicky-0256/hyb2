#!/usr/bin/awk -f
# Read a FASTA and its CT. With normalize=1, write the first mfold structure
# using the input name. Optional constraints and Vienna output must agree
# with the CT. Exit 42 only for a valid structure missing a requested constraint.
function invalid(reason) {
	bad = 1
	print "Invalid fold output: " reason > "/dev/stderr"
	exit 1
}
function number(value) {
	return value ~ /^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([Ee][+-]?[0-9]+)?$/ && tolower(sprintf("%g", value + 0)) !~ /inf|nan/
}
function rna(value) {
	value = toupper(value)
	gsub(/[[:space:]]/, "", value)
	gsub(/T/, "U", value)
	return value
}
function check_constraints(    line, result, fields, count, i, j, k, offset, matches, left, right) {
	if (constraints == "") return
	while ((result = (getline line < constraints)) > 0) {
		sub(/#.*/, "", line)
		sub(/^[[:space:]]+/, "", line)
		sub(/[[:space:]]+$/, "", line)
		if (line == "") continue
		if (normalize || first_only) gsub(/O/, "0", line)
		count = split(line, fields, /[[:space:]]+/)
		if ((normalize || first_only) && count == 3 && fields[1] == "P" &&
		    fields[2] ~ /^[0-9]+-[0-9]+$/ && fields[3] ~ /^[0-9]+-[0-9]+$/) {
			split(fields[2], left, "-"); split(fields[3], right, "-")
			if (left[1] < 1 || left[1] > left[2] || left[2] > n || right[1] < 1 || right[1] > right[2] || right[2] > n)
				incompatible = 1
			else for (i = left[1]+0; i <= left[2]; i++)
				if (partner[i] >= right[1] && partner[i] <= right[2]) incompatible = 1
			continue
		}
		if (count != 4 || fields[1] !~ /^[FP]$/ || fields[2] !~ /^[0-9]+$/ || fields[3] !~ /^[0-9]+$/ || fields[4] !~ /^[0-9]+$/)
			invalid("constraint " line)
		i = fields[2] + 0; j = fields[3] + 0; k = fields[4] + 0
		if (i < 1 || k < 1 || i+k-1 > n || j > n || (j && j-k+1 < 1)) { incompatible = 1; continue }
		for (offset = 0; offset < k; offset++) {
			matches = j ? partner[i+offset] == j-offset : partner[i+offset] != 0
			if ((fields[1] == "F") != matches) incompatible = 1
		}
	}
	if (result < 0) invalid("could not read constraints")
	close(constraints)
}
function check_vienna(dot,    header, bases, line, energy, id) {
	if (vienna == "") return
	if ((getline header < vienna) != 1 || (getline bases < vienna) != 1 || rna(bases) != seq ||
	    (getline line < vienna) != 1) invalid("missing or incorrect Vienna record")
	sub(/^>/, "", header); sub(/^[[:space:]]+/, "", header)
	split(header, id, /[[:space:]]+/)
	if (id[1] != ct_name) invalid("Vienna record name disagrees with CT")
	sub(/^[[:space:]]+/, "", line)
	if (substr(line, 1, n) != dot) invalid("Vienna structure disagrees with CT")
	energy = substr(line, n+1)
	if (!sub(/^[[:space:]]+\([[:space:]]*/, "", energy) || !sub(/[[:space:]]*\)[[:space:]]*$/, "", energy) || !number(energy))
		invalid("Vienna free energy")
	# b2ct rounds the source energy to one decimal place.
	if (energy - ct_energy > 0.050001 || ct_energy - energy > 0.050001) invalid("Vienna free energy disagrees with CT")
}
FILENAME == ARGV[1] {
	sub(/\r$/, "")
	if (/^>/) { sub(/^>/, ""); names[++records] = $1; next }
	if ($0 ~ /^[[:space:]]*$/) next
	if (!records) invalid("missing FASTA header")
	sequences[records] = sequences[records] rna($0)
	next
}
{ sub(/\r$/, "") }
!structures || rows == n {
	if (normalize && structures) next
	structures++
	if (first_only && structures > 1) invalid("extra mfold CT structure")
	source = records == 1 ? 1 : structures
	seq = sequences[source]; n = length(seq); rows = 0
	if (!n || $1 !~ /^[0-9]+$/ || $1 + 0 != n) invalid("CT sequence length")
	header = $0
	if (!sub(/^[[:space:]]*[0-9]+[[:space:]]+dG[[:space:]]*=[[:space:]]*/, "", header)) invalid("CT header")
	split(header, parts, /[[:space:]]+/)
	if (!number(parts[1])) invalid("CT free energy")
	ct_energy = parts[1] + 0
	ct_name = parts[2]
	if (normalize) printf "%d\tdG = %s\t%s\n", n, parts[1], names[source]
	next
}
{
	i = ++rows
	if (NF < 6) invalid("incomplete CT row " i)
	for (k = 1; k <= 6; k++) if (k != 2 && $k !~ /^[0-9]+$/) invalid("number in CT row " i)
	if ($1 + 0 != i || $3 + 0 != i-1 || $4 + 0 != (i < n ? i+1 : 0) || $6 + 0 != i) invalid("indices in CT row " i)
	if (rna($2) != substr(seq, i, 1)) invalid("base in CT row " i)
	j = $5 + 0
	if (j == i || j > n) invalid("partner in CT row " i)
	partner[i] = j
	if (normalize) print
	if (rows == n) {
		dot = ""; depth = 0
		for (i = 1; i <= n; i++) {
			j = partner[i]
			if (j && partner[j] != i) invalid("non-reciprocal pair in CT row " i)
			if (j > i) closing[++depth] = j
			else if (j && (!depth || closing[depth--] != i)) invalid("crossing pairs in CT row " i)
			dot = dot (j == 0 ? "." : (j > i ? "(" : ")"))
		}
		check_constraints()
		check_vienna(dot)
	}
}
END {
	if (bad) exit 1
	if (!structures || rows != n || (!normalize && !first_only && structures < records)) invalid("truncated CT structure")
	if (vienna != "") {
		while ((more = (getline line < vienna)) > 0) if (line ~ /[^[:space:]]/) invalid("extra Vienna record")
		if (more < 0) invalid("could not read Vienna output")
	}
	if (incompatible) exit 42
}
