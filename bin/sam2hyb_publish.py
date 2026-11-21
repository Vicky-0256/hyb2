"""Publish the three completed sam2hyb files from its private working directory."""
import fcntl
import os
from pathlib import Path
import signal
import sys


def publish(work, prefix, inputs):
    # hyb2 uses the primary HYB as its completion marker. Lock the containing
    # directory only during publication, so concurrent runs cannot mix results.
    outputs = [(work / "homodimers.hyb", Path(str(prefix) + "_homodimers.hyb")),
               (work / "stats.txt", Path(str(prefix) + "_hybrids.hyb_stats.txt")),
               (work / "result.hyb", Path(str(prefix) + ".hyb"))]
    directory = os.open(prefix.parent, os.O_RDONLY)
    try:
        fcntl.flock(directory, fcntl.LOCK_EX)
        for staged, target in outputs:
            if not staged.is_file():
                raise OSError("missing staged output: " + str(staged))
            if target.exists():
                if not target.is_file() or any(target.samefile(source) for source in inputs):
                    raise OSError("output conflicts with an input or is not a regular file: " + str(target))

        marker = work / ".publishing"
        marker.touch()
        try:
            # Invalidate the old completion marker before changing any sidecar.
            for staged, target in reversed(outputs):
                if os.path.lexists(target):
                    os.replace(target, staged.with_suffix(".previous"))
            for staged, target in outputs:
                os.replace(staged, target)
        except BaseException:
            # Restore the primary last. If recovery itself fails, the shell
            # retains this directory and its originals for recovery.
            for staged, target in outputs:
                previous = staged.with_suffix(".previous")
                if os.path.lexists(previous):
                    os.replace(previous, target)
                elif not staged.exists():
                    target.unlink()
            marker.unlink()
            raise
        marker.unlink()
    finally:
        os.close(directory)


if __name__ == "__main__":
    def interrupted(signum, frame):
        raise InterruptedError("publication interrupted")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    try:
        publish(Path.cwd(), Path(sys.argv[1]), [Path(name) for name in sys.argv[2:] if name and Path(name).exists()])
    except OSError as error:
        sys.exit("sam2hyb: " + str(error))
