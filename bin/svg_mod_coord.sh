#!/bin/bash
# Relabel VARNA's numbered bases in the coordinates of the selected windows.
set -e
input= x= y= len= prefix=
while getopts "i:x:y:l:p:" opt; do
	case "$opt" in i) input=$OPTARG;; x) x=$OPTARG;; y) y=$OPTARG;; l) len=$OPTARG;; p) prefix=$OPTARG;; *) exit 2;; esac
done
out=${input%.svg}_plot.svg
if [[ -n "$prefix" ]]; then out=$prefix.${out##*/}; fi
awk -v x="${x:-1}" -v y="$y" -v len="$len" '
	{
		if ($10~/text/ && $10!~/>[A-Z]</ && $6~/7.5/) {
			split($10, tags, "<"); split(tags[1], number, ">"); pos=number[2]+0
			if (y=="" || pos<=len) suffix=" >" pos+x-1 "</text>"
			else if (pos<=len+100) suffix=" " $10
			else suffix=" >" pos+y-len-101 "</text>"
			$10=" "; $0=$0 suffix
		}
		gsub(/stroke-width="1.0"/,"stroke-width=\"0.25\"")
		print
	}' "$input" > "$out"
