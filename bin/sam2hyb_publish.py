"""Commit staged conversion outputs, with the primary HYB (if any) last."""
import argparse
from contextlib import ExitStack
import fcntl
import os
from pathlib import Path
import signal
import sys


def publish(work, outputs, inputs):
    # Each staged file lives on its target filesystem. Lock each actual
    # destination directory once, in a consistent order across concurrent runs.
    with ExitStack() as stack:
        directories = {}
        for parent in {target.parent for _, target in outputs}:
            fd = os.open(parent, os.O_RDONLY)
            stack.callback(os.close, fd)
            info = os.fstat(fd)
            directories[info.st_dev, info.st_ino] = fd
        for key in sorted(directories):
            fcntl.flock(directories[key], fcntl.LOCK_EX)

        reserved = list(inputs)
        for staged, target in outputs:
            if staged.is_symlink() or not staged.is_file():
                raise OSError("missing staged output: " + str(staged))
            if os.path.lexists(target):
                if not target.is_file() or any(target.samefile(source) for source in reserved):
                    raise OSError("output conflicts with an input, another output, or is not a regular file: " + str(target))
                reserved.append(target)

        marker = work / ".publishing"
        marker.touch()
        # Back up beside the staged file, not in a shared work directory that
        # may be on a different mount from the reference being replaced.
        files = [(staged, target, staged.with_name(staged.name + ".previous")) for staged, target in outputs]
        try:
            # Invalidate the old completion marker before changing any sidecar.
            for staged, target, previous in reversed(files):
                if os.path.lexists(target):
                    os.replace(target, previous)
            for staged, target, previous in files:
                if os.path.lexists(target):
                    raise OSError("output name collision: " + str(target))
                os.replace(staged, target)
        except BaseException:
            # Restore the primary last. If recovery itself fails, the shell
            # retains all staging directories and their originals for recovery.
            for staged, target, previous in files:
                if os.path.lexists(previous):
                    os.replace(previous, target)
                elif not staged.exists():
                    target.unlink()
            marker.unlink()
            raise
        marker.unlink()


if __name__ == "__main__":
    def interrupted(signum, frame):
        raise InterruptedError("publication interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--protect", action="append", default=[])
    parser.add_argument("--work", required=True, type=Path)
    parser.add_argument("paths", nargs="+", type=Path, metavar="STAGED_OR_TARGET")
    args = parser.parse_args()
    if len(args.paths) % 2:
        parser.error("expected staged/target path pairs")
    try:
        publish(args.work, list(zip(args.paths[::2], args.paths[1::2])),
                [Path(name) for name in args.protect if name and Path(name).exists()])
    except OSError as error:
        sys.exit("sam2hyb: " + str(error))
