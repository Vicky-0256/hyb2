#!/bin/bash
# Map observed pair counts to the endpoints of each pair in the structure.
set -e
len= input= bps= tail= max= suffix= prefix=
while getopts "l:i:b:t:m:s:p:" opt; do
	case "$opt" in l) len=$OPTARG;; i) input=$OPTARG;; b) bps=$OPTARG;; t) tail=$OPTARG;;
		m) max=$OPTARG;; s) suffix=$OPTARG;; p) prefix=$OPTARG;; *) exit 2;; esac
done
[[ "$len" =~ ^[0-9]+$ && "$max" =~ ^[0-9]+$ && "${tail:-0}" =~ ^[0-9]+$ ]] || exit 2
prefix=${prefix:-${input%.basepair_scores.txt}}
stem=${bps##*/}; stem=${stem%.bps}
out=${prefix}__${stem}${suffix}.VARNA_scores.txt
awk -v len="$len" -v cap="$max" -v tail="${tail:-0}" '
	FILENAME==ARGV[1] {count[$1,$2]+=$3; count[$2,$1]+=$3; next}
	FNR>1 && ($1 SUBSEP $2) in count {
		value=count[$1,$2]; score[$1]=score[$2]=(value>cap ? cap : value)
	}
	END {start=(tail>0 && tail<len ? len-tail+1 : 1); for(i=start;i<=len;i++) print score[i]+0}' "$input" "$bps" > "$out"
