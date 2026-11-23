"""Portable runtime capability detection for HYB2 Desktop.

The module intentionally uses only Python's standard library so it can run in
an installer bootstrap, a development checkout, or a diagnostic command.
"""
import sys as _sys
from pathlib import Path as _Path

# Import roots of the folding package (``cplfold``) and the Hyb2 settings
# (``hyb2analysis``), set once for every runtime module so each is loaded
# under a single name per process.
for _root in (_Path(__file__).resolve().parents[2] / name for name in ("pipeline", "third_party/cplfold")):
    if str(_root) not in _sys.path:
        _sys.path.insert(0, str(_root))
del _root

__all__ = [
    "CapabilityMatrix",
    "CapabilityResult",
    "PlatformSnapshot",
    "ProbeResult",
    "RuntimeDetector",
    "detect_runtime",
    "validate_manifest",
    "validate_project_directory",
]


def __getattr__(name):
    # Lazy imports keep ``python -m desktop.runtime.detector`` free of the
    # runpy warning caused by importing the target module from __init__ first.
    if name in {"RuntimeDetector", "detect_runtime"}:
        from .detector import RuntimeDetector, detect_runtime
        return {"RuntimeDetector": RuntimeDetector, "detect_runtime": detect_runtime}[name]
    if name in {"CapabilityMatrix", "CapabilityResult", "PlatformSnapshot", "ProbeResult"}:
        from .model import CapabilityMatrix, CapabilityResult, PlatformSnapshot, ProbeResult
        return {"CapabilityMatrix": CapabilityMatrix, "CapabilityResult": CapabilityResult, "PlatformSnapshot": PlatformSnapshot, "ProbeResult": ProbeResult}[name]
    if name in {"validate_manifest", "validate_project_directory"}:
        from .project import validate_manifest, validate_project_directory
        return {"validate_manifest": validate_manifest, "validate_project_directory": validate_project_directory}[name]
    raise AttributeError(name)
