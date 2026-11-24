"""Task ids, run folders and input paths of bridge tasks: one copy of the rules.

Every task writes into ``<project>/runs/<task_id>/`` (created here, or made
by the shell and passed as ``run_dir``) and reads inputs that are either
project-relative or linked absolute files allowed by :mod:`trust`.  None of
these helpers follows a project-controlled symlink out of the project.

Two ways of creating ``runs/<task_id>`` are kept because callers report them
differently: :func:`secure_run_dir` (bridge operations; also re-checks
``runs`` after creating it) and :func:`real_child_dir` (analysis tasks; also
used for ``structures/`` and ``comparisons/``).
"""
from __future__ import annotations

import os
import uuid
from pathlib import Path
from typing import Any, Dict, List

from . import trust as _trust
from .name_matching import manual_matches
from .project import load_manifest, safe_relative_path

TASK_ID_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-"
MAX_HYB_FILES = 64


def safe_task_id(value: Any, prefix: str) -> str:
    """``value``, or ``prefix`` plus a random hex id when it is empty; at most
    64 characters, starting with a letter, from :data:`TASK_ID_CHARS`."""
    task_id = str(value or (prefix + uuid.uuid4().hex))
    if not task_id or len(task_id) > 64 or not task_id[0].isalpha() or any(c not in TASK_ID_CHARS for c in task_id):
        raise ValueError("task_id must be a safe identifier")
    return task_id


def real_child_dir(parent: Path, name: str, *, exist_ok: bool) -> Path:
    """``parent/name`` as a real directory, never following a symlink."""
    if parent.exists() and (parent.is_symlink() or not parent.is_dir()):
        raise ValueError(f"project {parent.name} directory must be a real directory")
    parent.mkdir(mode=0o700, exist_ok=True)
    target = parent / name
    if target.is_symlink():
        raise ValueError(f"{parent.name}/{name} must not be a symlink")
    if target.exists():
        if not exist_ok or not target.is_dir():
            raise FileExistsError(f"{parent.name}/{name} already exists")
        return target
    target.mkdir(mode=0o700)
    return target


def secure_run_dir(project: Path, task_id: str) -> Path:
    """Create ``runs/<task_id>`` without following a project-controlled symlink."""
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


def requested_run_dir(project: Path, requested: Any) -> Path:
    """A shell-created run folder: an existing direct child of ``runs``."""
    run_dir = Path(str(requested)).expanduser().resolve()
    if not run_dir.is_dir() or run_dir.parent != (project / "runs").resolve():
        raise ValueError("run_dir must be an existing direct child of project runs")
    return run_dir


def task_run_dir(project: Path, task_id: str, requested: Any) -> Path:
    """Bridge operations: ``requested`` when given, else :func:`secure_run_dir`."""
    if requested is not None:
        return requested_run_dir(project, requested)
    return secure_run_dir(project, task_id)


def resolve_run_dir(project: Path, task_id: str, requested: Any) -> Path:
    """Analysis tasks: ``requested`` when given, else ``runs/<task_id>`` by :func:`real_child_dir`."""
    if requested is not None:
        return requested_run_dir(project, requested)
    return real_child_dir(project / "runs", task_id, exist_ok=False)


def project_file(project: Path, relative: Any, label: str) -> Path:
    """A project-relative input that must stay inside the project."""
    if not safe_relative_path(relative):
        raise ValueError(f"{label} must be a project-relative path")
    target = (project / relative).resolve()
    if project not in target.parents or not target.is_file():
        raise ValueError(f"{label} does not exist in the project: {relative}")
    return target


def input_file(project: Path, value: Any, label: str) -> Path:
    """A project-relative path inside the project, or an absolute path (linked data)."""
    if not isinstance(value, str) or not value.strip():
        raise ValueError(f"{label} must be a non-empty path")
    if os.path.isabs(value) or value.startswith("~"):
        _trust.require(project, value, label)
        target = Path(value).expanduser().resolve()
    else:
        if not safe_relative_path(value):
            raise ValueError(f"{label} must be a safe project-relative or absolute path")
        target = (project / value).resolve()
        if project not in target.parents:
            raise ValueError(f"{label} escapes the project")
    if not target.is_file():
        raise ValueError(f"{label} does not exist: {value}")
    return target


def evidence_inputs(project: Path, request: Dict[str, Any]) -> Dict[str, Any]:
    """Resolve .hyb paths, the reference FASTA and gene_id from samples or explicit paths."""
    sample_ids = request.get("sample_ids")
    hyb_paths = request.get("hyb_paths")
    reference = request.get("reference")
    gene_id = request.get("gene_id")
    if gene_id is not None and (not isinstance(gene_id, str) or not gene_id.strip()):
        raise ValueError("gene_id must be a non-empty string")
    samples_used: List[Dict[str, Any]] = []
    paths: List[Path] = []
    manifest = load_manifest(project)
    references = {item["id"]: item for item in manifest.get("references") or []}
    reference_ids = set()
    entry = None
    if sample_ids:
        if not isinstance(sample_ids, list) or not all(isinstance(item, str) for item in sample_ids):
            raise ValueError("sample_ids must be a list of sample ids")
        samples = {sample.get("id"): sample for sample in manifest.get("samples") or [] if isinstance(sample, dict)}
        genes = set()
        for sample_id in sample_ids:
            sample = samples.get(sample_id)
            if sample is None:
                raise ValueError(f"unknown sample: {sample_id}")
            hyb = (sample.get("outputs") or {}).get("hyb")
            reads = sample.get("reads") or {}
            if not hyb and reads.get("format") == "hyb":
                hyb = reads.get("path")  # a linked or copied .hyb is its own output
            if not hyb:
                raise ValueError(f"sample {sample_id} has no .hyb output yet")
            paths.append(input_file(project, hyb, f"sample {sample_id} .hyb"))
            reference_ids.add(sample.get("reference_id"))
            if sample.get("gene_id"):
                genes.add(sample["gene_id"])
            samples_used.append({"id": sample_id, "hyb": str(paths[-1]), "reference_id": sample.get("reference_id"),
                                 "gene_id": sample.get("gene_id"), "condition": sample.get("condition")})
        if reference is None:
            if len(reference_ids) != 1 or None in reference_ids:
                raise ValueError("the selected samples must share one reference")
            entry = references.get(next(iter(reference_ids)))
            if not entry or not entry.get("path"):
                raise ValueError("the samples' reference is missing from the project")
            reference = entry["path"]
        if gene_id is None and len(genes) == 1:
            gene_id = next(iter(genes))
        elif gene_id is None and len(genes) > 1:
            raise ValueError("the selected samples have different gene_id values; give gene_id")
    if hyb_paths:
        if not isinstance(hyb_paths, list) or len(hyb_paths) > MAX_HYB_FILES:
            raise ValueError(f"hyb_paths must be a list of at most {MAX_HYB_FILES} paths")
        paths.extend(input_file(project, value, "hyb path") for value in hyb_paths)
    if not paths:
        raise ValueError("structure_run needs sample_ids or hyb_paths")
    if len(paths) > MAX_HYB_FILES:
        raise ValueError(f"at most {MAX_HYB_FILES} .hyb files per run")
    if reference is None:
        raise ValueError("structure_run needs a reference FASTA (reference) when no samples are given")
    reference_path = input_file(project, reference, "reference")
    if entry is None:
        # An explicit file uses its own registered matches, never those of a
        # different reference that happens to be assigned to the samples.
        same_file = [r for r in references.values() if project / r["path"] == reference_path]
        choices = [r for r in same_file if r["id"] in reference_ids] or same_file
        if len(choices) > 1:
            raise ValueError("the FASTA is registered as several references; select samples of one reference")
        entry = choices[0] if choices else None
    return {"hyb_paths": paths, "reference": reference_path, "gene_id": gene_id, "samples": samples_used,
            "matches": manual_matches(entry)}
