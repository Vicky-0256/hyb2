"""Process bridge used by the independent HYB2 Desktop entry point.

The Tauri layer can launch this module as a sidecar and exchange one JSON
request per invocation.  Keeping the protocol here makes the desktop app
testable without a GUI and avoids shell command construction.
"""
from __future__ import annotations

import argparse
import json
import sys
import tempfile
import uuid
import subprocess
import os
from pathlib import Path
from typing import Any

from .detector import detect_runtime
from .project import new_project, validate_project_directory, import_input, update_ui_state
import importlib.util

_RUNNER_PATH = Path(__file__).resolve().parents[1] / "task-runner" / "runner.py"
_RUNNER_SPEC = importlib.util.spec_from_file_location("hyb2_task_runner", _RUNNER_PATH)
_RUNNER_MODULE = importlib.util.module_from_spec(_RUNNER_SPEC)
assert _RUNNER_SPEC.loader is not None
sys.modules["hyb2_task_runner"] = _RUNNER_MODULE
_RUNNER_SPEC.loader.exec_module(_RUNNER_MODULE)
TaskRunner = _RUNNER_MODULE.TaskRunner
TaskSpec = _RUNNER_MODULE.TaskSpec

_TASK_ID_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"


def _safe_task_id(value: Any, prefix: str) -> str:
    task_id = str(value or (prefix + uuid.uuid4().hex))
    if not task_id or len(task_id) > 64 or not task_id[0].isalpha() or any(c not in _TASK_ID_CHARS for c in task_id):
        raise ValueError("task_id must be a safe identifier")
    return task_id


def _secure_run_dir(project: Path, task_id: str) -> Path:
    """Create a run directory without following a project-controlled symlink."""
    runs = project / "runs"
    if runs.exists() and (runs.is_symlink() or not runs.is_dir()):
        raise ValueError("project runs directory escapes project (must be a real directory)")
    runs.mkdir(mode=0o700, exist_ok=True)
    if runs.is_symlink() or runs.resolve() != runs:
        raise ValueError("project runs directory escapes project")
    target = runs / task_id
    if target.exists() or target.is_symlink():
        raise FileExistsError("task_id already exists")
    target.mkdir(mode=0o700)
    return target


def project_path(request: dict[str, Any]) -> Path:
    value = request.get("path")
    if not isinstance(value, str) or not value.strip():
        raise ValueError("project path must be a non-empty string")
    path = Path(value).expanduser().resolve()
    return path.parent if path.name == "project.json" and path.is_file() else path


def handle(request: dict[str, Any], *, repo_root: Path | None = None) -> dict[str, Any]:
    if not isinstance(request, dict):
        raise ValueError("bridge request must be a JSON object")
    operation = request.get("operation")
    if operation == "detect":
        return {"ok": True, "result": detect_runtime(repo_root).to_dict()}
    if operation in {"validate_project", "open_project"}:
        path = project_path(request)
        errors = validate_project_directory(path, check_files=bool(request.get("check_files", False)))
        if errors:
            return {"ok": False, "path": str(path), "errors": errors, "error": "; ".join(errors)}
        result = {"ok": True, "path": str(path), "errors": []}
        if operation == "open_project":
            result["manifest"] = json.loads((path / "project.json").read_text(encoding="utf-8"))
        return result
    if operation == "create_project":
        path = project_path(request)
        manifest = new_project(path, str(request.get("name", "")), description=str(request.get("description", "")))
        return {"ok": True, "manifest": str(manifest)}
    if operation == "cplfold_predict":
        # Execute the shared CPLfold implementation in the bundled Python
        # runtime and return the same result contract as cplfold.worker.mjs.
        sequence = request.get("sequence")
        if not isinstance(sequence, str) or not sequence.strip():
            raise ValueError("sequence must contain RNA bases A/C/G/U/T/N")
        sequence = "".join(sequence.upper().split()).replace("T", "U")
        if not sequence or any(c not in "ACGUN" for c in sequence):
            raise ValueError("sequence must contain RNA bases A/C/G/U/T/N")
        try:
            max_nt = int(os.environ.get("HYB2_CPLFOLD_MAX_NT", "1000"))
        except ValueError:
            max_nt = 1000
        if len(sequence) > max_nt:
            raise ValueError(f"sequence exceeds CPLfold safety limit ({max_nt} nt)")
        root = Path(repo_root or Path.cwd()).resolve()
        source = root / "web" / "cplfold" / "cplfold_web.py"
        if not source.is_file():
            return {"ok": False, "status": "unavailable", "error": "native CPLfold source is not bundled"}
        spec = importlib.util.spec_from_file_location("hyb2_native_cplfold", source)
        if spec is None or spec.loader is None:
            return {"ok": False, "status": "unavailable", "error": "native CPLfold module cannot be loaded"}
        module = importlib.util.module_from_spec(spec)
        # cplfold_web imports the vendored CPLfold module by its historical
        # top-level name; make both source layouts visible to that import.
        sys.path.insert(0, str(source.parent))
        sys.path.insert(0, str(root / "third_party" / "cplfold"))
        spec.loader.exec_module(module)
        params = request.get("parameters") if isinstance(request.get("parameters"), dict) else {}
        allow = params.get("allowPseudoknot", True)
        if not isinstance(allow, bool):
            raise ValueError("allowPseudoknot must be a boolean")
        payload = {
            "sequence": sequence,
            "evidenceMode": request.get("evidenceMode", "none"),
            "evidenceArms": request.get("evidenceArms", []),
            "beamSize": params.get("beamSize", params.get("beam", 20)),
            "maxPhase1": params.get("maxPhase1", params.get("max_phase1", 3)),
            "energyDelta": params.get("energyDelta", params.get("delta", 5)),
            "energyModel": params.get("energyModel", params.get("model", "DP09")),
            "alpha": params.get("alpha", 0.5),
            "beta": params.get("beta", 0),
            "allowPseudoknot": allow,
            # The shared result normaliser retains the browser's structural
            # safety bound; native task mode still has its own 1000 nt guard.
            "maxSequenceLength": min(max_nt, 500),
        }
        result = module.fold(payload)
        result["runtime"] = "HYB2 Desktop bundled Python/NumPy/Numba"
        result["native"] = True
        result["ok"] = True
        return result
    if operation == "import_input":
        path = project_path(request)
        errors = validate_project_directory(path)
        if errors: raise ValueError("invalid project: " + "; ".join(errors))
        source = request.get("source")
        if not isinstance(source, str) or not source.strip(): raise ValueError("input source must be a non-empty path")
        item = import_input(path, Path(source), role=str(request.get("role", "other")), label=str(request.get("label", "")))
        return {"ok": True, "path": str(path), "input": item}
    if operation == "update_ui_state":
        path = project_path(request)
        errors = validate_project_directory(path)
        if errors: raise ValueError("invalid project: " + "; ".join(errors))
        result = update_ui_state(path, request.get("ui_state", {}))
        return {"ok": True, "path": str(path), **result}
    if operation == "run_task":
        raw_path = request.get("path")
        if not isinstance(raw_path, str) or not raw_path.strip():
            raise ValueError("project path must be a non-empty string")
        path = Path(raw_path).expanduser().resolve()
        kind = str(request.get("kind", "validate_project"))
        if kind != "validate_project":
            raise ValueError("unsupported safe task kind: %s" % kind)
        task_id = _safe_task_id(request.get("task_id"), "task-")
        if not path.is_dir() or not (path / "project.json").is_file():
            raise ValueError("project directory with project.json does not exist: %s" % path)
        errors = validate_project_directory(path)
        if errors:
            raise ValueError("invalid project: " + "; ".join(errors))
        run_dir = _secure_run_dir(path, task_id)
        request_file = run_dir / "task-request.json"
        request_file.write_text(json.dumps({"operation": "validate_project", "path": str(path), "check_files": True}), encoding="utf-8")
        events: list[dict[str, object]] = [{"type": "queued", "taskId": task_id, "status": "queued"}]
        def collect(event: dict[str, object]) -> None:
            events.append(event)
        runtime_root = repo_root or Path(__file__).resolve().parents[2]
        spec = TaskSpec.from_values([sys.executable, "-m", "desktop.runtime.bridge", "--repo-root", str(runtime_root), "--request", str(request_file)], cwd=runtime_root, task_id=task_id)
        code = TaskRunner(collect).run(spec)
        (run_dir / "events.jsonl").write_text("".join(json.dumps(event, ensure_ascii=False) + "\n" for event in events), encoding="utf-8")
        result = {"ok": True, "task_id": task_id, "status": "succeeded" if code == 0 else "failed", "exit_code": code, "events": events, "run_dir": str(run_dir)}
        if code:
            result["error"] = "Project validation failed; see the task events for details."
        (run_dir / "status.json").write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        return result
    if operation == "cplfold_run":
        raw_path = request.get("project_path")
        sequence = request.get("sequence")
        if not isinstance(raw_path, str) or not raw_path.strip():
            raise ValueError("project path must be a non-empty string")
        if not isinstance(sequence, str) or not sequence.strip():
            raise ValueError("sequence must contain RNA bases A/C/G/U/T/N")
        sequence = "".join(sequence.upper().split()).replace("T", "U")
        if not sequence or any(c not in "ACGUN" for c in sequence):
            raise ValueError("sequence must contain RNA bases A/C/G/U/T/N")
        try:
            max_nt = int(os.environ.get("HYB2_CPLFOLD_MAX_NT", "1000"))
        except ValueError:
            max_nt = 1000
        if len(sequence) > max_nt:
            raise ValueError(f"sequence exceeds CPLfold safety limit ({max_nt} nt)")
        path = Path(raw_path).expanduser().resolve()
        errors = validate_project_directory(path)
        if errors: raise ValueError("invalid project: " + "; ".join(errors))
        task_id = _safe_task_id(request.get("task_id"), "cplfold-")
        requested_run_dir = request.get("run_dir")
        if requested_run_dir is not None:
            run_dir = Path(str(requested_run_dir)).expanduser().resolve()
            runs_root = (path / "runs").resolve()
            if not run_dir.is_dir() or os.path.commonpath([str(run_dir), str(runs_root)]) != str(runs_root) or run_dir.parent != runs_root:
                raise ValueError("run_dir must be an existing direct child of project runs")
        else:
            run_dir = _secure_run_dir(path, task_id)
        root = Path(repo_root or Path.cwd()).resolve(); executable = root / "bin" / "cplfold"
        if not executable.is_file():
            return {"ok": False, "status": "unavailable", "error": "native CPLfold runtime is not bundled", "task_id": task_id}
        sequence_file = run_dir / "sequence.fa"; sequence_file.write_text(">sequence\n" + sequence + "\n", encoding="utf-8")
        # Invoke the script through the interpreter that launched this bridge.
        # The vendored ``bin/cplfold`` shebang is intentionally not trusted:
        # ``/usr/bin/env python3`` would otherwise escape a bundled runtime and
        # silently run the host Python.
        argv = [sys.executable, str(executable), "--sequence-file", str(sequence_file)]
        params = request.get("parameters") if isinstance(request.get("parameters"), dict) else {}
        allowed = {"alpha": "--alpha", "beta": "--beta", "beam": "--beam", "delta": "--delta", "max_phase1": "--max-phase1", "max_phase2": "--max-phase2", "model": "--model"}
        bounds = {"alpha": (-100.0, 100.0), "beta": (-100.0, 100.0), "beam": (1, 5000), "delta": (0.0, 100.0), "max_phase1": (1, 1000), "max_phase2": (1, 1000)}
        for key, flag in allowed.items():
            if key in params:
                value = params[key]
                if not isinstance(value, (int, float, str)) or isinstance(value, bool): raise ValueError("invalid CPLfold parameter: " + key)
                if key in bounds:
                    try: numeric = float(value)
                    except (TypeError, ValueError): raise ValueError("invalid CPLfold parameter: " + key)
                    lo, hi = bounds[key]
                    if numeric < lo or numeric > hi or (key not in {"alpha", "beta", "delta"} and numeric != int(numeric)):
                        raise ValueError("CPLfold parameter out of bounds: " + key)
                argv.extend([flag, str(value)])
        if params.get("no_pseudoknot") is True: argv.append("--no-pseudoknot")
        bonus = request.get("bonus_matrix")
        if bonus is not None:
            if isinstance(bonus, list):
                if len(bonus) > 5000 or any(not isinstance(row, list) or len(row) > 5000 or any(not isinstance(value, (int, float)) or isinstance(value, bool) for value in row) for row in bonus):
                    raise ValueError("bonus_matrix rows must be bounded numeric arrays")
                n = len(bonus)
                if any(len(row) != n for row in bonus): raise ValueError("bonus_matrix must be square")
                entries = ["# HYB2 CPLfold bonus matrix", f"# sequence_length={len(sequence)}", "# coordinate_system=prepared-sequence-1-based-inclusive", "prepared_position_1\tprepared_position_2\tlog1p_gaussian_hyb_bonus"]
                entries.extend(f"{i+1}\t{j+1}\t{float(value):.8g}" for i, row in enumerate(bonus) for j, value in enumerate(row) if float(value) != 0.0 and i < j)
                bonus = "\n".join(entries) + "\n"
            if not isinstance(bonus, str) or len(bonus) > 20_000_000: raise ValueError("bonus_matrix must be a bounded text matrix or numeric matrix")
            bonus_file = run_dir / "bonus.matrix"; bonus_file.write_text(bonus, encoding="utf-8"); argv.extend(["--bonus-matrix-file", str(bonus_file)])
        output = run_dir / "cplfold.out"
        argv.extend(["--output", str(output), "--quiet"])
        env = os.environ.copy()
        env.setdefault("HYB2_CPLFOLD_MAX_NT", "1000")
        # Keep JIT artifacts inside the project run.  This is writable in
        # packaged apps and prevents a hostile/shared global cache from being
        # reused across projects.
        cache_root = run_dir / "numba-cache"
        cache_root.mkdir(mode=0o700, exist_ok=True)
        env["XDG_CACHE_HOME"] = str(cache_root)
        try:
            completed = subprocess.run(argv, cwd=root, capture_output=True, text=True, timeout=900, check=False, env=env)
        except subprocess.TimeoutExpired as error:
            result = {"ok": False, "status": "failed", "task_id": task_id, "run_dir": str(run_dir), "error": "CPLfold timed out", "exit_code": None, "stdout": (error.stdout or "")[-20000:], "stderr": (error.stderr or "")[-20000:]}
            (run_dir / "status.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
            return result
        result = {"ok": completed.returncode == 0, "status": "succeeded" if completed.returncode == 0 else "failed", "task_id": task_id, "run_dir": str(run_dir), "output": str(output) if output.exists() else None, "stdout": completed.stdout[-20000:], "stderr": completed.stderr[-20000:], "exit_code": completed.returncode}
        (run_dir / "status.json").write_text(json.dumps(result, indent=2), encoding="utf-8")
        return result
    raise ValueError("unsupported bridge operation: %s" % operation)


def main() -> int:
    parser = argparse.ArgumentParser(description="HYB2 Desktop JSON bridge")
    parser.add_argument("--repo-root", type=Path)
    parser.add_argument("--request", type=Path, help="JSON request file; stdin is used when omitted")
    parser.add_argument("--validate-project", type=Path, help="validate a project directory")
    args = parser.parse_args()
    try:
        raw = args.request.read_text(encoding="utf-8") if args.request else (json.dumps({"operation": "validate_project", "path": str(args.validate_project), "check_files": True}) if args.validate_project else input())
        result = handle(json.loads(raw), repo_root=args.repo_root)
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as error:
        result = {"ok": False, "error": str(error)}
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    return 0 if result.get("ok") else 1


if __name__ == "__main__":
    raise SystemExit(main())
