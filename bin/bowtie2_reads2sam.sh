# Shared by the three bowtie2_*2sam entry points, which set kind before sourcing.
set -eo pipefail

usage() { echo "usage: ${0##*/} -i reads.$kind -d reference.fasta -o output-prefix" >&2; exit 2; }
input= reference= out=
while getopts "i:d:o:" option; do
    [[ -n "$OPTARG" ]] || usage
    path=$OPTARG
    [[ "$path" == /* ]] || path=$PWD/$path
    case "$option" in
        i) input=$path ;;
        d) reference=$path ;;
        o) out=$path ;;
        *) usage ;;
    esac
done
shift $((OPTIND - 1))
[[ $# == 0 && -f "$input" && -r "$input" && -f "$reference" && -r "$reference" && -n "${out##*/}" ]] || usage
if [[ "$out.sam" -ef "$out.blast.err" ]]; then
    echo "SAM and diagnostic outputs refer to the same file: $out" >&2; exit 1
fi
for target in "$out.sam" "$out.blast.err"; do
    if [[ -e "$target" && ( ! -f "$target" || "$target" -ef "$input" || "$target" -ef "$reference" ) ]]; then
        echo "Cannot replace mapping output: $target" >&2; exit 1
    fi
done

# HYB2_THREADS, else all online CPUs (nproc: Linux and Git-Bash; getconf/sysctl: macOS).
threads=${HYB2_THREADS:-$(nproc 2>/dev/null || getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)}
if [[ ! "$threads" =~ ^[0-9]+$ ]] || (( 10#$threads == 0 )); then
    echo "HYB2_THREADS must be a positive whole number: $threads" >&2; exit 2
fi

# Mapping settings (gk-dev defaults, Kudla U2 benchmark on the Brr2 CLASH data):
# cutadapt trims the 3' adapter before collapsing (HYB2_ADAPTER=none skips it;
# collapsed FASTA is never trimmed), then Bowtie2 -L HYB2_SEED_LENGTH
# --score-min HYB2_SCORE_MIN (legacy 16 / L,18,0) plus HYB2_BOWTIE2_ARGS.
adapter=${HYB2_ADAPTER-TGGAATTCTCGGGTGCCAAGG}
[[ "$adapter" == [Nn][Oo][Nn][Ee] ]] && adapter=
min_length=${HYB2_MIN_LENGTH:-18}
seed_length=${HYB2_SEED_LENGTH:-10}
score_min=${HYB2_SCORE_MIN:-L,15,0}
read -r -a bowtie2_extra <<< "${HYB2_BOWTIE2_ARGS:-}"
for setting in "HYB2_MIN_LENGTH=$min_length" "HYB2_SEED_LENGTH=$seed_length"; do
    if [[ ! "${setting#*=}" =~ ^[0-9]+$ ]]; then
        echo "${setting%%=*} must be a whole number: ${setting#*=}" >&2; exit 2
    fi
done
if [[ -n "$adapter" ]] && ! command -v cutadapt > /dev/null; then
    echo "cutadapt not found: install it (conda install -c bioconda cutadapt) or set HYB2_ADAPTER=none" >&2; exit 1
fi

# Nothing beside the reads or reference is scratch. Stage the SAM on its final
# filesystem so publishing it takes one rename, even when replacing an old SAM.
bin_dir=$(cd -- "$(dirname -- "$0")" && pwd)
source "$bin_dir/cli_stage.sh"
hyb2_fix_path
work=$(mktemp -d "${out%/*}/.hyb2-map.XXXXXXXX")
trap hyb2_cleanup_stage EXIT
trap 'exit 1' HUP INT TERM
cd -- "$work"

collapse_fastq() { solexa2fasta.awk | fasta2tab.awk | make_comp_fasta.pl; }
# Identical reads become one read named <rank>_<count>; the later steps take the
# read count from that name. FASTA reads that all have such a name already
# (<rank>_<count> or <rank>-<barcodes>_<count>) are mapped as they are.
fasta_is_collapsed() {
    LC_ALL=C awk '/^>/ {
        name = substr($0, 2); sub(/^[[:space:]]+/, "", name); sub(/[[:space:]].*$/, "", name)
        found = name ~ /^[0-9]+(-[0-9]+)?_[0-9]+$/
        if (!found) exit
    } END { exit !found }' "$1"
}
trim_adapters() {  # trim_adapters <format>: cutadapt writes trimmed.<format>
    echo "Trimming adapters (report: $out.cutadapt.log)..."
    cutadapt -a "$adapter" -m "$((10#$min_length))" -j "$((10#$threads))" -o "trimmed.$1" "$input" > cutadapt.log 2>&1 \
        || { echo "cutadapt failed on $input: $(tail -n 1 cutadapt.log)" >&2; return 1; }
    local processed written
    processed=$(sed -n 's/^Total reads processed: *\([0-9,]*\).*/\1/p' cutadapt.log | tr -d ,)
    written=$(sed -n 's/^Reads written (passing filters): *\([0-9,]*\).*/\1/p' cutadapt.log | tr -d ,)
    if (( ${processed:-0} > 0 && ${written:-0} == 0 )); then
        echo "No reads are left after adapter trimming: all $processed reads are shorter than $min_length nt" \
             "without the adapter (set HYB2_MIN_LENGTH lower, or HYB2_ADAPTER=none)." >&2
        return 1
    fi
}
case "$kind" in
    fasta)
        if fasta_is_collapsed "$input"; then
            LC_ALL=C awk -f "$bin_dir/mapping_fasta.awk" < "$input" > reads.fasta
        else
            reads=$input
            if [[ -n "$adapter" ]]; then trim_adapters fasta; reads=trimmed.fasta; fi
            LC_ALL=C awk -v reads=1 -f "$bin_dir/mapping_fasta.awk" < "$reads" | fasta2tab.awk | make_comp_fasta.pl > reads.fasta
        fi ;;
    fastq|fastq.gz)
        if [[ -n "$adapter" ]]; then
            trim_adapters fastq; collapse_fastq < trimmed.fastq > reads.fasta
        elif [[ "$kind" == fastq ]]; then
            collapse_fastq < "$input" > reads.fasta
        else
            gunzip -c < "$input" | collapse_fastq > reads.fasta
        fi ;;
esac

# Even six complete adjacent shards can describe different reference names or
# sequences. Build from the supplied file; never modify or trust those shards.
echo "Making database..."
LC_ALL=C awk -f "$bin_dir/mapping_fasta.awk" < "$reference" > reference.fasta
bowtie2-build reference.fasta index > /dev/null

echo "Bowtie2 Mapping..."
status=0
# Do not let a missing private index fall back to a global index of that name.
unset BOWTIE2_INDEXES
# --reorder keeps the SAM in read order, so reruns give the same output.
bowtie2 -D 20 -R 3 -N 0 -L "$((10#$seed_length))" -k 20 --local -i S,1,0.50 --score-min "$score_min" --ma 1 --np 0 \
    --mp 2,2 --rdg 5,1 --rfg 5,1 "${bowtie2_extra[@]}" --reorder -p "$((10#$threads))" -x index -f reads.fasta > mapped.sam 2> mapping.err || status=$?
publish=(python3 "$bin_dir/sam2hyb_publish.py" --work="$work" --protect="$input" --protect="$reference")
if (( status != 0 )) || [[ ! -s mapped.sam ]]; then
    "${publish[@]}" --protect="$out.sam" -- mapping.err "$out.blast.err"
    (( status == 0 )) || exit "$status"
    echo "Bowtie2 produced an empty SAM." >&2; exit 1
fi
cutadapt_report=()
[[ -f cutadapt.log ]] && cutadapt_report=(cutadapt.log "$out.cutadapt.log")
"${publish[@]}" -- mapping.err "$out.blast.err" "${cutadapt_report[@]}" mapped.sam "$out.sam"
echo "Mapping Concluded"
