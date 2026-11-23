# Private helpers for the shell commands that stage and publish result files.
# Keep PATH overrides (including empty entries) relative to the invocation
# directory, not to whichever private directory a tool subsequently enters.
hyb2_fix_path() {
    local rest=$PATH: dir
    PATH=
    while [[ -n "$rest" ]]; do
        dir=${rest%%:*}; rest=${rest#*:}
        [[ "$dir" == /* ]] || dir=$PWD/$dir
        PATH=${PATH:+$PATH:}$dir
    done
    export PATH="$PATH:$bin_dir"
}

hyb2_cleanup_stage() {
    if [[ -e "$work/.publishing" ]]; then
        echo "${0##*/}: interrupted publication; recover staged files and originals in $work" >&2
    else
        rm -rf -- "$work"
    fi
}
