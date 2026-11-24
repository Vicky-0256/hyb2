#!/bin/bash
#shortcut to VARNAcmd.jar: prints the path that hyb2_install recorded in VARNA.dir
#(beside this script); prints nothing before installation
dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
if [[ -f "$dir/VARNA.dir" ]]; then cat -- "$dir/VARNA.dir"; fi
