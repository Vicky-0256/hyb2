"""Small dependency-free manifest and path checks for the desktop prototype.

The JSON Schema remains the normative format. These checks are deliberately
duplicated here because the bootstrap and diagnostic environments cannot rely
on a JSON Schema package being installed.
"""

import hashlib
import json
import re
import uuid
import shutil
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional

_SHA256 = re.compile(r"^[0-9a-fA-F]{64}$")
_ID = re.compile(r"^[A-Za-z][A-Za-z0-9._-]{0,63}$")
_SCHEMA = re.compile(r"^[0-9]+\.[0-9]+$")
_DATE = re.compile(r"^\d{4}-\d{2}-\d{2}T")


def safe_relative_path(value: Any) -> bool:
    if not isinstance(value, str) or not value or "\x00" in value:
        return False
    normal = value.replace("\\", "/")
    if normal.startswith("/") or re.match(r"^[A-Za-z]:", normal):
        return False
    return all(part not in ("", ".", "..") for part in normal.split("/"))


def _required(obj: Dict[str, Any], names: Iterable[str], errors: List[str], where: str) -> None:
    for name in names:
        if name not in obj:
            errors.append("%s missing %s" % (where, name))


def validate_manifest(manifest: Any) -> List[str]:
    """Return human-readable validation errors; an empty list means valid."""
    errors: List[str] = []
    if not isinstance(manifest, dict):
        return ["manifest must be an object"]
    _required(manifest, ("format", "schema_version", "project_id", "name", "created_at", "updated_at", "inputs", "runs", "artifacts", "application", "runtime"), errors, "manifest")
    if manifest.get("format") != "hyb2-project": errors.append("manifest.format must be hyb2-project")
    if not isinstance(manifest.get("schema_version"), str) or not _SCHEMA.match(manifest.get("schema_version", "")): errors.append("manifest.schema_version must be major.minor")
    if not isinstance(manifest.get("project_id"), str) or not re.match(r"^[0-9a-fA-F-]{36}$", manifest.get("project_id", "")): errors.append("manifest.project_id must be a UUID")
    for key in ("created_at", "updated_at"):
        if not isinstance(manifest.get(key), str) or not _DATE.match(manifest.get(key, "")): errors.append("manifest.%s must be an ISO timestamp" % key)
    if not isinstance(manifest.get("name"), str) or not manifest.get("name"): errors.append("manifest.name must be non-empty")
    app = manifest.get("application")
    if not isinstance(app, dict) or app.get("name") != "HYB2 Desktop" or not isinstance(app.get("version"), str): errors.append("manifest.application must contain HYB2 Desktop and version")
    for key, required in (("inputs", ("id", "role", "path", "format", "sha256", "size_bytes")), ("runs", ("id", "kind", "status", "input_ids", "parameters", "output_paths")), ("artifacts", ("path", "kind", "sha256", "size_bytes"))):
        value = manifest.get(key)
        if not isinstance(value, list):
            errors.append("manifest.%s must be an array" % key)
            continue
        ids = set()
        for index, item in enumerate(value):
            where = "manifest.%s[%d]" % (key, index)
            if not isinstance(item, dict):
                errors.append(where + " must be an object")
                continue
            _required(item, required, errors, where)
            if "id" in required and (not isinstance(item.get("id"), str) or not _ID.match(item.get("id", ""))): errors.append(where + ".id is invalid")
            if "path" in item and not safe_relative_path(item.get("path")): errors.append(where + ".path must be relative and traversal-free")
            for path_key in ("output_paths", "log_paths"):
                if path_key in item:
                    if not isinstance(item[path_key], list):
                        errors.append(where + "." + path_key + " must be an array")
                    else:
                        for path in item[path_key]:
                            if not safe_relative_path(path): errors.append(where + "." + path_key + " contains an unsafe path")
            if "sha256" in item and (not isinstance(item.get("sha256"), str) or not _SHA256.match(item.get("sha256", ""))): errors.append(where + ".sha256 must be 64 hex characters")
            if "size_bytes" in item and (type(item.get("size_bytes")) is not int or item.get("size_bytes") < 0): errors.append(where + ".size_bytes must be a non-negative integer")
            if isinstance(item.get("id"), str):
                if item["id"] in ids: errors.append(where + ".id is duplicated")
                ids.add(item["id"])
    runtime = manifest.get("runtime")
    if not isinstance(runtime, dict) or not isinstance(runtime.get("capabilities"), list): errors.append("manifest.runtime.capabilities must be an array")
    return errors


def validate_project_directory(root: Path, check_files: bool = False) -> List[str]:
    root = Path(root).resolve()
    manifest_path = root / "project.json"
    if not manifest_path.is_file(): return ["project.json is missing"]
    try: manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc: return ["project.json cannot be read: %s" % exc]
    errors = validate_manifest(manifest)
    if check_files and not errors:
        paths = []
        for item in manifest.get("inputs", []): paths.append(item.get("path"))
        # Artifact entries are provenance references and normally repeat a run
        # output path; they are checked for existence below but are not treated
        # as duplicate archive members.
        for item in manifest.get("artifacts", []): paths.append(item.get("path"))
        for run in manifest.get("runs", []): paths.extend(run.get("output_paths", [])); paths.extend(run.get("log_paths", []))
        seen = set()
        artifact_paths = {item.get("path") for item in manifest.get("artifacts", [])}
        for relative in paths:
            if not safe_relative_path(relative):
                continue
            normal = relative.replace("\\", "/")
            if normal in seen and relative not in artifact_paths:
                errors.append("duplicate project path: %s" % relative)
            seen.add(normal)
            candidate = (root / normal).resolve()
            if root not in candidate.parents and candidate != root:
                errors.append("project path escapes root: %s" % relative)
            elif not candidate.is_file():
                errors.append("missing project file: %s" % relative)
    return errors


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with Path(path).open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""): digest.update(chunk)
    return digest.hexdigest()


def import_input(root: Path, source: Path, *, role: str = "other", label: str = "") -> Dict[str, Any]:
    """Copy an input into ``inputs/`` and append its portable manifest record.

    The source is read only; project references always point at the copied file.
    This makes an opened project self-contained and prevents later edits to an
    arbitrary external file from silently changing a saved analysis.
    """
    root = Path(root).expanduser().resolve()
    source = Path(source).expanduser().resolve()
    if not source.is_file():
        raise ValueError("input file does not exist: %s" % source)
    manifest_path = root / "project.json"
    if not manifest_path.is_file():
        raise ValueError("project.json is missing")
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    errors = validate_manifest(manifest)
    if errors:
        raise ValueError("invalid project: " + "; ".join(errors))
    if role not in {"hyb", "fasta", "fastq", "sam", "reference", "constraints", "other"}:
        raise ValueError("invalid input role")
    inputs_dir = root / "inputs"
    inputs_dir.mkdir(mode=0o700, exist_ok=True)
    stem = re.sub(r"[^A-Za-z0-9._-]+", "_", source.name).strip("._") or "input"
    destination = inputs_dir / stem
    if destination.exists():
        digest = sha256_file(source)
        for item in manifest["inputs"]:
            if item.get("path") == "inputs/" + stem and item.get("sha256") == digest:
                return item
        destination = inputs_dir / (destination.stem + "-" + uuid.uuid4().hex[:8] + destination.suffix)
    shutil.copy2(source, destination)
    relative = destination.relative_to(root).as_posix()
    suffix = source.name.lower()
    formats = {".hyb": "hyb", ".fasta": "fasta", ".fa": "fasta", ".fastq": "fastq", ".fq": "fastq", ".sam": "sam", ".ct": "ct", ".dbn": "dbn", ".tsv": "tsv", ".csv": "csv"}
    fmt = formats.get(source.suffix.lower(), "other")
    now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    item = {"id": "input-" + uuid.uuid4().hex[:12], "role": role, "path": relative,
            "format": fmt, "sha256": sha256_file(destination), "size_bytes": destination.stat().st_size,
            "label": (label or source.stem)[:200], "imported_at": now}
    manifest["inputs"].append(item)
    manifest["updated_at"] = now
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return item


def read_input(root: Path, relative: str, *, limit: int = 50_000_000) -> Dict[str, Any]:
    """Read a project input for the integrated desktop analysis workspace."""
    if not safe_relative_path(relative):
        raise ValueError("input path must be relative and traversal-free")
    root = Path(root).expanduser().resolve()
    path = (root / relative).resolve()
    if root not in path.parents or not path.is_file():
        raise ValueError("project input does not exist")
    if path.stat().st_size > limit:
        raise ValueError("project input exceeds desktop preview limit")
    return {"path": relative, "name": path.name, "content": path.read_text(encoding="utf-8", errors="replace")}


def save_analysis_run(root: Path, result: Dict[str, Any]) -> Dict[str, Any]:
    """Persist a compact structure result and register it in project.json."""
    if not isinstance(result, dict): raise ValueError("analysis result must be an object")
    root = Path(root).expanduser().resolve()
    manifest_path = root / "project.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    errors = validate_manifest(manifest)
    if errors: raise ValueError("invalid project: " + "; ".join(errors))
    run_id = "run-" + uuid.uuid4().hex[:12]
    run_dir = root / "runs" / run_id
    run_dir.mkdir(parents=True, exist_ok=False)
    output = run_dir / "structure-result.json"
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    relative = output.relative_to(root).as_posix()
    now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    manifest["runs"].append({"id": run_id, "kind": str(result.get("algorithm", "structure")), "status": "succeeded", "input_ids": [], "parameters": {"engine": result.get("engine"), "constraintMode": result.get("constraintMode")}, "output_paths": [relative], "log_paths": []})
    manifest["artifacts"].append({"path": relative, "kind": "structure-result", "sha256": sha256_file(output), "size_bytes": output.stat().st_size})
    manifest["updated_at"] = now
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return {"run_id": run_id, "path": relative}


def update_ui_state(root: Path, state: Dict[str, Any]) -> Dict[str, Any]:
    """Persist bounded, JSON-serializable UI state without absolute paths."""
    if not isinstance(state, dict):
        raise ValueError("ui state must be an object")
    encoded = json.dumps(state, ensure_ascii=False)
    if len(encoded) > 256_000:
        raise ValueError("ui state is too large")
    def check(value: Any) -> None:
        if isinstance(value, str):
            normal = value.replace("\\", "/")
            if "\x00" in value or normal.startswith("/") or re.match(r"^[A-Za-z]:/", normal) or any(part == ".." for part in normal.split("/")):
                raise ValueError("ui state cannot contain absolute or traversal paths")
        elif isinstance(value, dict):
            for key, child in value.items():
                if not isinstance(key, str): raise ValueError("ui state keys must be strings")
                check(child)
        elif isinstance(value, list):
            for child in value: check(child)
        elif value is not None and not isinstance(value, (bool, int, float)):
            raise ValueError("ui state must contain JSON scalar values")
    check(state)
    root = Path(root).expanduser().resolve(); manifest_path = root / "project.json"
    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    errors = validate_manifest(manifest)
    if errors: raise ValueError("invalid project: " + "; ".join(errors))
    now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    manifest["ui_state"] = state; manifest["updated_at"] = now
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return {"updated_at": now, "ui_state": state}


def new_project(root: Path, name: str, *, description: str = "", app_version: str = "0.1.0") -> Path:
    """Create an empty portable project directory and return its manifest path.

    This is intentionally a small filesystem API for the desktop bridge.  It
    never accepts an input path as a project member; callers import files via
    a later, hash-recording operation.
    """
    root = Path(root).expanduser().resolve()
    if not isinstance(name, str) or not name.strip():
        raise ValueError("project name must be non-empty")
    if root.exists() and any(root.iterdir()):
        raise ValueError("project directory must be empty or absent")
    root.mkdir(parents=True, exist_ok=True)
    for directory in ("inputs", "runs", "exports"):
        (root / directory).mkdir()
    now = datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    manifest = {
        "format": "hyb2-project", "schema_version": "1.0",
        "project_id": str(uuid.uuid4()), "name": name.strip(),
        "description": description, "created_at": now, "updated_at": now,
        "application": {"name": "HYB2 Desktop", "version": app_version},
        "inputs": [], "runs": [], "artifacts": [],
        "runtime": {"protocol_version": "1.0", "detected_at": now, "detector_version": "unknown",
                     "platform": {"os": "unknown", "architecture": "unknown", "release": "unknown", "wsl": False, "wsl_distributions": []},
                     "capabilities": []},
        "settings": {"max_parallel_tasks": 1}, "ui_state": {"route": "overview", "filters": {}}, "extensions": {},
    }
    manifest_path = root / "project.json"
    manifest_path.write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
    return manifest_path
