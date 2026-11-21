#!/bin/bash
set -eo pipefail
coords= scores= prefix=
while getopts "c:i:s:p:" opt; do
	case "$opt" in c|i) coords=$OPTARG;; s) scores=$OPTARG;; p) prefix=$OPTARG;; *) exit 2;; esac
done
while IFS= read -r line || [[ -n "$line" ]]; do
	IFS=$'\t' read -r ct offset length tail <<< "$line"
	if [[ -z "$tail" ]]; then read -r ct offset length tail <<< "$line"; fi
	[[ -n "$ct" && "$offset" =~ ^[0-9]+$ && "$length" =~ ^[0-9]+$ && "$tail" =~ ^[0-9]+$ ]] || { echo "Invalid structure coordinates: $line" >&2; exit 1; }
	bps=${ct%.ct}.bps
	awk -v offset="$offset" 'BEGIN {OFS="\t"} NR==1 {print $1,$5} NR>1 && $1<$5 {print $1+offset,$5+offset}' "$ct" > "$bps"
	args=(-m 1000000 -l "$length" -t "$tail" -i "$scores" -b "$bps")
	if [[ -n "$prefix" ]]; then args+=(-p "${prefix%.hyb}"); fi
	make_VARNA_scores_2.sh "${args[@]}"
done < "$coords"
