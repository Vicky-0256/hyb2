"""Command line entry point for ``python3 -m hyb2analysis``."""
from __future__ import annotations

import sys

from .compare_cli import main

if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
