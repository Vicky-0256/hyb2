"""The reference-ID rules of ``hyb2pipe`` for the shell commands in ``bin/``.

    python -m hyb2pipe.reference_cli check reference.fasta   # exit 0: rewrite IDs, 1: use as is
    python -m hyb2pipe.reference_cli format reference.fasta  # formatted FASTA on stdout

``bin/hyb2`` and ``bin/fasta_hyb2_formatting`` call this so that they rewrite
reference IDs exactly as ``hyb2pipe`` does (``inputs.needs_formatting`` and
``inputs.format_reference``, which use ``hyb2analysis.reference``).  Both
commands exit with status 2 and a message for an unusable reference (for
example duplicate IDs).
"""
from __future__ import annotations

import sys
from pathlib import Path
from typing import List, Optional

from . import inputs


def check_fasta(path: Path) -> None:
    """Refuse a file that does not start (after blank lines) with a FASTA header."""
    for line in inputs.iter_lines(path):
        line = inputs.unindent_fasta_header(line)
        if line.startswith(">"):
            return
        if line.strip():
            raise ValueError("text before the first FASTA header")
    raise ValueError("no FASTA header")


def main(argv: Optional[List[str]] = None) -> int:
    args = sys.argv[1:] if argv is None else argv
    if len(args) != 2 or args[0] not in ("check", "format"):
        print("usage: python -m hyb2pipe.reference_cli check|format reference.fasta", file=sys.stderr)
        return 2
    command, path = args[0], Path(args[1])
    try:
        if command == "check":
            check_fasta(path)
            return 0 if inputs.needs_formatting(path) else 1
        lines = inputs.format_reference(inputs.iter_lines(path))
        inputs.check_formatted(lines)
    except (OSError, ValueError) as error:
        print("Unusable reference FASTA %s: %s" % (path, error), file=sys.stderr)
        return 2
    out = sys.stdout.buffer
    out.write("\n".join(lines).encode("utf-8"))
    out.flush()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
