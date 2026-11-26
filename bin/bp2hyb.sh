#!/bin/bash
# bp2hyb.sh [trim_fraction] < pairs > helices
# Merge scored base pairs (i<TAB>j<TAB>count) that touch on a diagonal into
# helices (.hyb rows), strongest first; a helix's score is the sum of its counts.
#
# Merging joins neighbouring pairs whatever their counts, so one weak pair can
# lengthen a strong helix and, as a forced constraint, block the helix next to
# it. Pairs are therefore dropped from either end of a helix while their count
# is below trim_fraction (default 0.05) of the helix's best pair; pairs inside
# are kept. This is hyb2 gk-dev's bp2hyb; a trim_fraction of 0 gives Lau's script.
set -eo pipefail

trim=${1:-0.05}
[[ "$trim" =~ ^(0|1|0?\.[0-9]+)$ ]] || { echo "bp2hyb.sh: trim_fraction must be a number from 0 to 1: $trim" >&2; exit 2; }
pairs=$(mktemp "${TMPDIR:-/tmp}/hyb2-bp2hyb.XXXXXX")
trap 'rm -f -- "$pairs"' EXIT
trap 'exit 1' HUP INT TERM
cat > "$pairs"

sort -k1,1n "$pairs" | awk -v nm="RNA" 'BEGIN{OFS="\t"}{print NR,".",$3,nm,".",".",$1,$1,".",nm,".",".",$2,$2,"."}' | combine_hyb_merge_touching.pl |
	awk -v trim="$trim" -v pairs="$pairs" 'BEGIN {
		OFS="\t"
		while ((getline row < pairs) > 0) { split(row, pair, "\t"); count[pair[1], pair[2]]=pair[3] }
	}
	{
		# a helix pairs $7+k with $14-k
		last=$8-$7; best=0
		for (k=0; k<=last; k++) {
			at[k]=(($7+k, $14-k) in count) ? count[$7+k, $14-k] : 0
			if (at[k]>best) best=at[k]
		}
		low=0; high=last
		if (trim>0) {
			while (low<high && at[low]<trim*best) low++
			while (high>low && at[high]<trim*best) high--
		}
		if (low>0 || high<last) {
			total=0
			for (k=low; k<=high; k++) total+=at[k]
			start=$7; end=$14
			$7=start+low; $8=start+high; $13=end-high; $14=end-low; $3=total
		} else $3=$3*(1+$8-$7)
		print
	}' | sort -k3,3nr
