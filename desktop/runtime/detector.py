"""Cross-platform, no-dependency runtime and capability detector.

This is a reference implementation for the Tauri runtime bridge. It never
passes a command through a shell; all probes are executable plus an argument
array. A detector result is diagnostic metadata and must not replace command
allow-listing in the task runner.
"""

import argparse
import json
import os
import platform
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

from .model import CapabilityMatrix, CapabilityResult, PlatformSnapshot, ProbeResult, utc_now

DETECTOR_VERSION = "0.1.0"
DEFAULT_CPLFOLD_MAX_NT = 1000


def cplfold_max_nt() -> int:
    """Native CPLfold length limit (``HYB2_CPLFOLD_MAX_NT``, default 1000)."""
    try:
        value = int(os.environ.get("HYB2_CPLFOLD_MAX_NT", str(DEFAULT_CPLFOLD_MAX_NT)))
    except ValueError:
        return DEFAULT_CPLFOLD_MAX_NT
    return value if value > 0 else DEFAULT_CPLFOLD_MAX_NT


def _normalise_os(name: str) -> str:
    value = name.lower()
    if value.startswith("win"):
        return "windows"
    if value == "darwin" or value.startswith("mac"):
        return "macos"
    if value.startswith("linux"):
        return "linux"
    return "unknown"


def _version(text: str) -> Optional[str]:
    match = re.search(r"(?<!\d)(\d+(?:\.\d+){1,3}(?:[-+][0-9A-Za-z.-]+)?)", text)
    return match.group(1) if match else None


class RuntimeDetector:
    def __init__(self, repo_root: Optional[Path] = None, timeout_seconds: float = 3.0):
        self.repo_root = Path(repo_root).resolve() if repo_root else Path(__file__).resolve().parents[2]
        self.timeout_seconds = timeout_seconds

    def detect(self) -> CapabilityMatrix:
        platform_info = self._platform()
        probes: List[ProbeResult] = []
        probes.append(self._python_probe())
        probes.append(self._python_package_probe("numpy", "language.numpy"))
        probes.append(self._python_package_probe("numba", "language.numba"))
        probes.append(self._python_package_probe("RNA", "language.viennarna"))
        for command, probe_id, kind in (
            ("bash", "shell.bash", "command"),
            ("perl", "language.perl", "command"),
            ("Rscript", "language.r", "command"),
            ("java", "language.java", "command"),
            ("samtools", "tool.samtools", "command"),
            ("RNAfold", "tool.rnafold", "command"),
            ("RNAcofold", "tool.rnacofold", "command"),
        ):
            probes.append(self._command_probe(command, probe_id, kind))
        probes.append(self._bowtie2_probe(("bowtie2-align-s", "bowtie2"), "tool.bowtie2"))
        probes.append(self._bowtie2_probe(("bowtie2-build-s", "bowtie2-build"), "tool.bowtie2-build"))
        probes.extend(self._repo_probes())
        probes.extend(self._native_runtime_probes())
        probes.extend(self._execution_probes())
        probes.append(self._wsl_probe(platform_info))
        probes.append(self._deseq_probe(probes))
        capabilities = self._capabilities(platform_info, probes)
        return CapabilityMatrix(utc_now(), DETECTOR_VERSION, platform_info, probes, capabilities)

    def _platform(self) -> PlatformSnapshot:
        os_id = _normalise_os(sys.platform)
        return PlatformSnapshot(
            os=os_id,
            architecture=platform.machine() or "unknown",
            release=platform.release() or "unknown",
            machine=platform.platform() or "unknown",
            python=platform.python_version(),
        )

    def _python_probe(self) -> ProbeResult:
        return ProbeResult("language.python", "available", "embedded", sys.executable, platform.python_version(), "Detector Python runtime")

    def _python_package_probe(self, package: str, probe_id: str) -> ProbeResult:
        try:
            completed = subprocess.run(
                [sys.executable, "-c", f"import {package}; print(getattr({package}, '__version__', 'installed'))"],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                text=True,
                timeout=self.timeout_seconds,
                check=False,
            )
            output = (completed.stdout or "").strip()
            if completed.returncode == 0:
                return ProbeResult(probe_id, "available", "python-package", sys.executable, _version(output) or output, f"Python package {package} is installed")
            return ProbeResult(probe_id, "unavailable", "python-package", sys.executable, summary=f"Python package {package} is not installed")
        except (OSError, subprocess.SubprocessError) as exc:
            return ProbeResult(probe_id, "degraded", "python-package", sys.executable, summary=f"Python package {package} probe failed", error=str(exc))

    def _command_probe(self, command: str, probe_id: str, kind: str) -> ProbeResult:
        path = shutil.which(command)
        if not path:
            return ProbeResult(probe_id, "unavailable", kind, summary="Command was not found on PATH")
        try:
            completed = subprocess.run([path, "--version"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=self.timeout_seconds, check=False)
            output = (completed.stdout or "").strip()
            return ProbeResult(probe_id, "available", kind, path, _version(output), output[:240])
        except (OSError, subprocess.SubprocessError) as exc:
            return ProbeResult(probe_id, "degraded", kind, path, summary="Command exists but version probe failed", error=str(exc))

    def _bowtie2_probe(self, names: Sequence[str], probe_id: str) -> ProbeResult:
        """Bundled ``bin/`` binaries first (the -s binaries need no Perl), then PATH."""
        suffix = ".exe" if os.name == "nt" else ""
        for name in names:
            bundled = self.repo_root / "bin" / (name + suffix)
            if bundled.is_file() and os.access(bundled, os.X_OK):
                return self._command_probe(str(bundled), probe_id, "bundled")
        for name in names:
            if shutil.which(name):
                return self._command_probe(name, probe_id, "command")
        return ProbeResult(probe_id, "unavailable", "command", summary="Bowtie2 was not bundled or found on PATH")

    def _repo_probes(self) -> List[ProbeResult]:
        results: List[ProbeResult] = []
        candidates = (
            ("hyb2.cli", "bin/hyb2", "script"),
            ("hyb2.fold", "bin/hyb2_fold", "script"),
            ("hyb2.sam_to_hyb", "bin/sam2hyb", "script"),
            ("cplfold.native", "bin/cplfold", "script"),
            ("varna.jar", "VARNA/build/jar/VARNA.jar", "jar"),
            ("varna.command.jar", "VARNA/build/jar/VARNAcmd.jar", "jar"),
        )
        for probe_id, relative, kind in candidates:
            path = self.repo_root / relative
            if path.is_file():
                results.append(ProbeResult(probe_id, "available", kind, str(path), summary="Repository component found"))
            else:
                results.append(ProbeResult(probe_id, "unavailable", kind, summary="Repository component was not found"))
        return results

    def _native_runtime_probes(self) -> List[ProbeResult]:
        """Probe only a declared portable resource, never the host venv."""
        # In development ``repo_root`` is the checkout root, while the Tauri
        # packaged bridge is launched with the resource root itself.  Accept
        # both layouts; otherwise a packaged app would probe
        # ``native/desktop/resources/native`` and always report unavailable.
        root = self.repo_root
        if not (root / "native-manifest.json").is_file():
            root = root / "desktop/resources/native"
        manifest_path = root / "native-manifest.json"
        try:
            manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return [ProbeResult("native.runtime", "unavailable", "native-runtime", summary="No native-manifest.json")]
        if manifest.get("portable") is not True or manifest.get("mode") != "standalone":
            return [ProbeResult("native.runtime", "unavailable", "native-runtime", summary="Native resources are development-only")]
        rel = manifest.get("python_executable")
        if not isinstance(rel, str) or Path(rel).is_absolute() or ".." in Path(rel).parts:
            return [ProbeResult("native.runtime", "unavailable", "native-runtime", summary="Manifest Python path is unsafe")]
        executable = root / rel
        if not executable.is_file():
            return [ProbeResult("native.runtime", "unavailable", "native-runtime", summary="Bundled Python executable is missing")]
        out = []
        for package in ("numpy", "numba"):
            probe_id = "native." + package
            try:
                result = subprocess.run([str(executable), "-I", "-c", f"import {package}; print({package}.__version__)"], cwd=root, capture_output=True, text=True, timeout=self.timeout_seconds, check=False)
                out.append(ProbeResult(probe_id, "available" if result.returncode == 0 else "unavailable", "native-package", str(executable), _version(result.stdout.strip()), summary="Bundled package imported" if result.returncode == 0 else "Bundled package import failed"))
            except (OSError, subprocess.SubprocessError) as error:
                out.append(ProbeResult(probe_id, "degraded", "native-package", str(executable), summary="Probe failed", error=str(error)))
        try:
            # Keep startup detection cheap: a full CPLfold fold is deliberately
            # exercised by release preflight/task execution, while ``--help``
            # verifies the bundled interpreter and script are runnable without
            # blocking the desktop UI for tens of seconds.
            with tempfile.TemporaryDirectory(prefix="hyb2-native-cplfold-") as cache:
                env = os.environ.copy()
                env["NUMBA_CACHE_DIR"] = cache
                env["PYTHONDONTWRITEBYTECODE"] = "1"
                result = subprocess.run([
                    str(executable), str(root / "bin/cplfold"), "--help",
                ], cwd=root, env=env, capture_output=True, text=True,
                    timeout=max(self.timeout_seconds, 10), check=False)
            out.append(ProbeResult("native.cplfold", "available" if result.returncode == 0 else "unavailable", "native-execution", str(root / "bin/cplfold"), summary="Bundled CPLfold executed" if result.returncode == 0 else "Bundled CPLfold failed"))
        except (OSError, subprocess.SubprocessError) as error:
            out.append(ProbeResult("native.cplfold", "degraded", "native-execution", summary="Probe failed", error=str(error)))
        return [ProbeResult("native.runtime", "available", "native-runtime", str(executable), summary="Portable native runtime declared")] + out

    def _execution_probes(self) -> List[ProbeResult]:
        results = []
        assets = self.repo_root / "web/vendor/viennarna"
        complete = all((assets / name).is_file() and (assets / name).stat().st_size > 0
                       for name in ("vienna-rna.js", "vienna-rna.wasm", "build-manifest.json"))
        results.append(ProbeResult("web.vienna_assets", "degraded" if complete else "unavailable", "assets",
                                   summary="ViennaRNA files present; browser execution unverified" if complete else "ViennaRNA generated assets missing"))
        cases = [
            ("numba.jit", ["-c", "import numpy,numba\n@numba.njit\ndef total(x): return x.sum()\nassert total(numpy.arange(10)) == 45\nassert total.nopython_signatures"], False),
            ("cplfold.execution", [str(self.repo_root / "bin/cplfold"), "--sequence", "AAAAA", "--quiet"], True),
        ]
        for probe_id, arguments, disable_jit in cases:
            env = os.environ.copy()
            env.pop("NUMBA_DISABLE_JIT", None)
            env["PYTHONDONTWRITEBYTECODE"] = "1"
            if disable_jit:
                env["NUMBA_DISABLE_JIT"] = "1"
            with tempfile.TemporaryDirectory(prefix="hyb2-runtime-probe-") as cache:
                env["NUMBA_CACHE_DIR"] = cache
                try:
                    result = subprocess.run([sys.executable, *arguments], env=env,
                                            capture_output=True, text=True,
                                            timeout=max(self.timeout_seconds, 10), check=False)
                    results.append(ProbeResult(probe_id, "available" if result.returncode == 0 else "unavailable",
                                               "execution", sys.executable,
                                               summary="Execution smoke passed" if result.returncode == 0 else "Execution smoke failed",
                                               error=result.stderr[-500:] if result.returncode else None))
                except (OSError, subprocess.SubprocessError) as error:
                    results.append(ProbeResult(probe_id, "degraded", "execution", sys.executable,
                                               summary="Execution could not be verified", error=str(error)))
        return results

    def _wsl_probe(self, platform_info: PlatformSnapshot) -> ProbeResult:
        command = shutil.which("wsl.exe") or shutil.which("wsl")
        if not command:
            return ProbeResult("platform.wsl", "unavailable", "wsl", summary="WSL executable was not found")
        try:
            completed = subprocess.run([command, "--list", "--quiet"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, timeout=self.timeout_seconds, check=False)
            distributions = [line.strip().replace("\x00", "") for line in (completed.stdout or "").splitlines() if line.strip()]
            platform_info.wsl = bool(distributions) or completed.returncode == 0
            platform_info.wsl_distributions = distributions
            return ProbeResult("platform.wsl", "available" if platform_info.wsl else "degraded", "wsl", command, summary="WSL distributions: " + (", ".join(distributions) or "none"))
        except (OSError, subprocess.SubprocessError) as exc:
            return ProbeResult("platform.wsl", "degraded", "wsl", command, summary="WSL exists but could not be queried", error=str(exc))

    def _deseq_probe(self, probes: Sequence[ProbeResult]) -> ProbeResult:
        rscript = next((item for item in probes if item.id == "language.r" and item.status == "available"), None)
        if not rscript or not rscript.executable:
            return ProbeResult("r.deseq2", "unavailable", "r-package", summary="Rscript is unavailable")
        try:
            completed = subprocess.run([rscript.executable, "-e", "cat(as.character(packageVersion('DESeq2')))"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, timeout=self.timeout_seconds, check=False)
            version = (completed.stdout or "").strip()
            if completed.returncode == 0 and version:
                return ProbeResult("r.deseq2", "available", "r-package", rscript.executable, version, "DESeq2 package is installed")
            return ProbeResult("r.deseq2", "unavailable", "r-package", rscript.executable, summary="Rscript is available but DESeq2 is not installed")
        except (OSError, subprocess.SubprocessError) as exc:
            return ProbeResult("r.deseq2", "degraded", "r-package", rscript.executable, summary="DESeq2 probe failed", error=str(exc))

    @staticmethod
    def _index(probes: Iterable[ProbeResult]) -> Dict[str, ProbeResult]:
        return {probe.id: probe for probe in probes}

    def _capabilities(self, platform_info: PlatformSnapshot, probes: Sequence[ProbeResult]) -> List[CapabilityResult]:
        p = self._index(probes)

        def status(ids: Sequence[str], fallback: str = "unavailable") -> str:
            values = [p.get(item).status if p.get(item) else fallback for item in ids]
            if all(value == "available" for value in values):
                return "available"
            if any(value == "available" for value in values):
                return "degraded"
            return "unavailable"

        def executable(ids: Sequence[str]) -> Optional[str]:
            for item in ids:
                if p.get(item) and p[item].executable:
                    return p[item].executable
            return None

        return [
            CapabilityResult("web.analysis", "available", "browser", "HYB parsing, filtering, maps, regions, comparison and reports are provided by web/.", requires=[]),
            CapabilityResult("web.structure", p.get("web.vienna_assets").status if p.get("web.vienna_assets") else "unavailable", "browser", "Browser structure engine requires generated assets and a browser execution check.", requires=["web.vienna_assets"]),
            CapabilityResult("pipeline.hyb2", status(["hyb2.cli", "language.perl"]), "hybrid", "HYB2 command-line analysis from the repository.", executable=executable(["hyb2.cli"]), requires=["hyb2.cli", "language.perl"], remediation="Install Perl or select a configured WSL/Conda runtime."),
            CapabilityResult("pipeline.sam_to_hyb", status(["hyb2.sam_to_hyb", "language.perl"]), "hybrid", "SAM to HYB conversion and filtering.", executable=executable(["hyb2.sam_to_hyb"]), requires=["hyb2.sam_to_hyb", "language.perl"], remediation="Install Perl or select a configured WSL/Conda runtime."),
            CapabilityResult("pipeline.python", "available" if (self.repo_root / "pipeline/hyb2pipe/__init__.py").is_file() else "unavailable", "native", "Pure-Python SAM/FASTQ to HYB pipeline (no Perl, awk or shell required).", executable=str(self.repo_root / "pipeline/hyb2pipe"), requires=[], remediation="Rebuild the desktop runtime with the pipeline package staged.", details={"inputs": ["sam", "fastq", "fastq.gz", "fasta"], "mapping_requires": "pipeline.mapping"}),
            CapabilityResult("pipeline.energy", "available" if p.get("language.viennarna") and p["language.viennarna"].status == "available" else "unavailable", "native", "Hybrid folding energy (hyb2pipe -e 1) through the ViennaRNA Python package.", version=p.get("language.viennarna").version if p.get("language.viennarna") else None, requires=["language.viennarna"], remediation="Bundle the ViennaRNA 2.7.2 wheel in the desktop runtime."),
            CapabilityResult("pipeline.mapping", status(["tool.bowtie2", "tool.bowtie2-build"]), "hybrid", "FASTQ mapping with Bowtie2.", executable=executable(["tool.bowtie2"]), requires=["tool.bowtie2", "tool.bowtie2-build"], remediation="Install Bowtie2 or enable WSL with a HYB2 environment."),
            CapabilityResult("structure.vienna_native", status(["tool.rnafold", "tool.rnacofold"]), "native", "Native RNAfold/RNAcofold structure calculations.", executable=executable(["tool.rnafold", "tool.rnacofold"]), requires=["tool.rnafold", "tool.rnacofold"], remediation="Install ViennaRNA; browser WASM remains available for supported sequence sizes."),
            CapabilityResult("structure.cplfold_native", status(["native.runtime", "native.numpy", "native.cplfold"]), "native", "Long-sequence CPLfold through the bundled/native Python and NumPy runtime.", version=p.get("native.numpy").version if p.get("native.numpy") else None, executable=executable(["native.cplfold"]), requires=["native.runtime", "native.numpy", "native.cplfold"], remediation="Install or bundle Python, NumPy, and the CPLfold runtime.", details={"max_sequence_length": cplfold_max_nt()}),
            CapabilityResult("structure.cplfold_numba", status(["native.runtime", "native.numba", "native.cplfold"]), "native", "Numba JIT acceleration for the native CPLfold runtime.", version=p.get("native.numba").version if p.get("native.numba") else None, executable=p.get("native.numba").executable if p.get("native.numba") else None, requires=["native.runtime", "native.numba", "native.cplfold"], remediation="Install or bundle Numba; NumPy mode remains available without JIT acceleration."),
            CapabilityResult("structure.varna", status(["varna.jar", "language.java"]), "native", "VARNA structure rendering through Java.", executable=executable(["varna.jar", "varna.command.jar", "language.java"]), requires=["varna.jar", "language.java"], remediation="Install Java or build VARNA jars."),
            CapabilityResult("statistics.deseq2", p.get("r.deseq2").status if p.get("r.deseq2") else "unavailable", "native", "DESeq2 interaction statistics.", version=p.get("r.deseq2").version if p.get("r.deseq2") else None, executable=p.get("r.deseq2").executable if p.get("r.deseq2") else None, requires=["r.deseq2"], remediation="Install R and the DESeq2 package."),
            CapabilityResult("platform.wsl", p.get("platform.wsl").status if p.get("platform.wsl") else "unavailable", "wsl", "Windows Subsystem for Linux adapter for the original shell pipeline.", executable=p.get("platform.wsl").executable if p.get("platform.wsl") else None, requires=["platform.wsl"], remediation="Install and initialize WSL with a HYB2 environment."),
        ]


def detect_runtime(repo_root: Optional[Path] = None) -> CapabilityMatrix:
    return RuntimeDetector(repo_root=repo_root).detect()


def main(argv: Optional[Sequence[str]] = None) -> int:
    parser = argparse.ArgumentParser(description="Detect HYB2 Desktop runtime capabilities")
    parser.add_argument("--repo-root", type=Path, help="HYB2 repository root (defaults to this checkout)")
    parser.add_argument("--pretty", action="store_true", help="Pretty-print JSON")
    args = parser.parse_args(argv)
    matrix = detect_runtime(args.repo_root)
    print(json.dumps(matrix.to_dict(), indent=2 if args.pretty else None, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
