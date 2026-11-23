"""Run the public POSIX CLI release tests with the real folding backends."""
import shutil
import sys
import unittest
from pathlib import Path


def main():
    required = ("RNAfold", "RNAcofold", "b2ct", "mfold", "mfold_datdir", "bowtie2", "bowtie2-build", "node")
    missing = [name for name in required if shutil.which(name) is None]
    if missing:
        print("Missing CLI release dependencies: " + ", ".join(missing), file=sys.stderr)
        return 2

    root = Path(__file__).resolve().parents[1]
    sys.path.insert(0, str(root))
    suite = unittest.defaultTestLoader.discover(str(root / "desktop/runtime/tests"), "test_*cli*.py")
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    if result.skipped or not result.testsRun:
        print("Every public CLI release test must run.", file=sys.stderr)
        return 1
    return int(not result.wasSuccessful())


if __name__ == "__main__":
    sys.exit(main())
