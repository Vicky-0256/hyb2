#!/usr/bin/awk -f
# Map cross-strand CT pairs back to the source HYB arms.
BEGIN { OFS="\t" }
function fail(message) {bad=1; print message > "/dev/stderr"; exit 1}
/dG/ {
	if (arms != "") {
		if ((getline header < arms) != 1 || (getline sequence < arms) != 1 ||
		    split(substr(header,2), a, /&>/) != 2 || $NF != (a[1] "-" a[2]))
			fail("Cofold CT does not match the input read arms")
		left=a[1]; right=a[2]
	} else {
		# Compatibility for standalone use with only the CT.
		if (!match($NF, /_[0-9]+_[0-9]+-/)) fail("Missing read coordinates in CT header")
		left=substr($NF,1,RSTART+RLENGTH-2); right=substr($NF,RSTART+RLENGTH)
	}
	x=split(left,i,"_"); y=split(right,j,"_")
	start1=i[x-1]; end1=i[x]; start2=j[y-1]
	len1=end1-start1+1
	next
}
$1<=len1 && $5>len1 {print start1+$1-1, start2+$5-len1-1}
END {
	if (!bad && arms!="" && (getline header < arms) != 0) fail("Missing cofold CT record")
}
