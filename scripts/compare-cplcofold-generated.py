#!/usr/bin/env python3
"""Reproducible generated two-RNA comparisons against local LinearCoFold."""
from __future__ import annotations

import argparse
from collections import Counter
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import math
import os
from pathlib import Path
import platform
import random
import re
import shlex
import subprocess
import sys
import tempfile
import traceback


ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(__file__).resolve()
HELPER = ROOT / "scripts/compare-cplcofold-linearcofold.py"
PARAMETERS = dict(alpha=0.0, beta=0.0, allow_pseudoknot=False,
                  energy_delta=0.0, max_phase1=1, verbose=False)
BEAMS = (0, 1, 20, 100)
ATOL = 1e-7


def strict_loads(text):
    def reject(value):
        raise ValueError("Nonfinite JSON constant: " + value)
    def finite_float(value):
        number = float(value)
        if not math.isfinite(number):
            raise ValueError("JSON number exceeds finite range")
        return number
    return json.loads(text, parse_constant=reject, parse_float=finite_float)


def json_text(value):
    return json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n"


def comparison_module():
    spec = importlib.util.spec_from_file_location("generated_comparison_helpers", HELPER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def generate_fixtures(seed, count, real_path):
    rng, cases, seen = random.Random(seed), [], set()
    def random_rna(length):
        return "".join(rng.choice("ACGU") for _ in range(length))
    def add(category, a, b, name=None):
        if (a, b) in seen:
            return
        seen.add((a, b))
        cases.append(dict(id=name or f"generated_{len(cases) + 1:03d}", category=category,
                          sequence_a=a, sequence_b=b, biological_provenance_verified=False))
    # Put representative edge cases first, then fill out composition and size coverage.
    add("adjacent_gc", "G", "C")
    add("unbound_homopolymers", "AAAAA", "AAAAA")
    add("gc_rich", "GCGCGC", "GCGCGC")
    add("au_rich", "AAAAUUUU", "AAAAUUUU")
    add("independent_hairpins", "GGGAAACCC", "GGGAAACCC")
    for a, b in ((1, 40), (5, 60), (30, 60), (80, 60)):
        add("asymmetric", random_rna(a), random_rna(b), f"asymmetric_{a}_{b}")
    add("repeat_hairpins", "GGGAAACCC" * 4, "CCCUUUGGG" * 4)
    for a in "ACGU":
        for b in "ACGU":
            add("all_single_bases", a, b)
    for a, b in ((1, 5), (5, 1), (2, 2), (2, 5), (5, 2), (3, 3), (3, 5), (5, 3), (4, 4), (5, 5)):
        add("short_1_to_5", random_rna(a), random_rna(b))
    for a, b in (("A", "A"), ("A", "U"), ("U", "U"), ("C", "C"),
                 ("C", "G"), ("G", "G"), ("G", "U"), ("U", "G")):
        add("homopolymers", a * 20, b * 24)
    for a, b in (("GC", "CG"), ("AU", "UA"), ("GGAU", "AUCC"), ("GGGAAA", "UUUCCC")):
        add("repeats", a * 6, b * 7)
    complement = str.maketrans("ACGU", "UGCA")
    for length in (5, 10, 20, 30, 40):
        a = random_rna(length)
        add("reverse_complement", a, a.translate(complement)[::-1])
    while len(cases) < count:
        a = rng.randint(1, 80)
        b = rng.randint(1, min(80, 140 - a))
        add("random", random_rna(a), random_rna(b))
    if len(cases) != count:
        raise ValueError("Requested synthetic count is smaller than the fixed coverage panel")
    source = None
    if real_path is not None:
        raw = real_path.read_bytes()
        panel = strict_loads(raw)
        if len(panel.get("cases", [])) != 2:
            raise ValueError("Expected the two pinned local U4/U6 window fixtures")
        for case in panel["cases"]:
            cases.append(dict(case, category="pinned_real_window"))
        source = dict(path=str(real_path.resolve()), sha256=hashlib.sha256(raw).hexdigest(),
                      notes="The two windows come from the same RNAs, not independent biological samples.")
    return dict(schema_version=1, generator=dict(seed=seed, synthetic_count=count,
                alphabet="ACGU", maximum_synthetic_strand_length=80, maximum_total_length=140),
                real_fixture_source=source, cases=cases)


def select_cases(cases, pilot):
    if pilot == 0 or pilot >= len(cases):
        return cases
    if pilot == 1:
        return cases[:1]
    # Evenly spaced selection samples the small, biased, random and real cases.
    return [cases[round(i * (len(cases) - 1) / (pilot - 1))] for i in range(pilot)]


def source_manifest():
    paths = [SCRIPT, HELPER]
    paths.extend(path for path in (ROOT / "third_party/cplfold").rglob("*")
                 if path.is_file() and "__pycache__" not in path.parts and "tests" not in path.parts
                 and (path.suffix == ".py" or "energy_params" in path.parts))
    return {str(path.relative_to(ROOT)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(paths)}


def parse_reference(case, beam, completed, helper):
    if completed.returncode != 0:
        raise ValueError(f"Reference exited with status {completed.returncode}")
    sequences = re.findall(r"^([ACGU]+&[ACGU]+)$", completed.stdout, re.MULTILINE)
    matches = re.findall(r"^([.()]+&[.()]+)\s+\(([+-]?\d+(?:\.\d+)?)\)$", completed.stdout, re.MULTILINE)
    if len(sequences) != 1 or len(matches) != 1:
        raise ValueError("Expected exactly one reference sequence and prediction")
    a, b = case["sequence_a"], case["sequence_b"]
    actual, (structure, energy_text) = sequences[0], matches[0]
    if actual not in (a + "&" + b, b + "&" + a):
        raise ValueError("Reference changed a nucleotide sequence")
    helper.validate_structure(actual, structure)
    energy = helper.finite_energy(float(energy_text))
    return dict(case_id=case["id"], beam=beam, category=case["category"], listed_sequence=a + "&" + b,
                compared_sequence=actual, lengths=[len(part) for part in actual.split("&")],
                reference_swapped_strands=actual != a + "&" + b,
                reference_structure=structure, reference_energy=energy,
                reference_stdout=completed.stdout, reference_stderr=completed.stderr)


def worker(config_path, output_path):
    helper = comparison_module()
    sys.path.insert(0, str(ROOT / "third_party/cplfold"))
    from CPLfold import cofold
    import CPLcofold_parser
    from Utils.cofold_energy import CofoldEnergy
    import numba
    import numpy as np
    if numba.config.DISABLE_JIT:
        raise RuntimeError("Generated comparison requires actual Numba JIT")
    config = strict_loads(config_path.read_text())
    evaluator, results = CofoldEnergy(), []
    for index, row in enumerate(config["rows"], 1):
        record = dict(row)
        if "error" not in row:
            try:
                a, b = row["compared_sequence"].split("&")
                candidates = cofold(a, b, beam_size=row["beam"], **PARAMETERS)
                if len(candidates) != 1:
                    raise ValueError("Expected exactly one public API prediction")
                prediction = candidates[0]
                for key in ("energy", "effective_energy", "phase1_energy", "score"):
                    helper.finite_energy(prediction[key])
                reference_rescore = evaluator.compute_energy(row["compared_sequence"], row["reference_structure"], len(a))["energy"]
                prediction_rescore = evaluator.compute_energy(row["compared_sequence"], prediction["structure"], len(a))["energy"]
                record.update(helper.compare_prediction(row["compared_sequence"], prediction,
                    row["reference_structure"], row["reference_energy"], reference_rescore, prediction_rescore))
                record["prediction"] = prediction
                record["search_scoring_equal"] = abs(prediction["score"] - prediction["energy"]) < ATOL
                if not record["search_scoring_equal"]:
                    record.update(status="search_score_mismatch", comparison_ok=False)
            except Exception as error:
                record.update(status="python_error", comparison_ok=False,
                              error=dict(type=type(error).__name__, message=str(error), traceback=traceback.format_exc()))
        results.append(record)
        if index == 1 or index % 10 == 0 or index == len(config["rows"]):
            print(f"Python {index}/{len(config['rows'])}: {row['case_id']} beam={row['beam']} {record['status']}", flush=True)
    signatures = {name: [str(signature) for signature in value.nopython_signatures]
                  for name, value in vars(CPLcofold_parser).items()
                  if hasattr(value, "nopython_signatures") and value.nopython_signatures}
    payload = dict(results=results, python=sys.version, numpy=np.__version__, numba=numba.__version__,
                   numba_disabled=bool(numba.config.DISABLE_JIT), nopython_signatures=signatures,
                   parser_module=str(Path(CPLcofold_parser.__file__).resolve()))
    def numpy_scalar(value):
        if isinstance(value, np.generic):
            return value.item()
        raise TypeError("Unsupported prediction value: " + type(value).__name__)
    output_path.write_text(json.dumps(payload, indent=2, sort_keys=True, allow_nan=False,
                                     default=numpy_scalar) + "\n")


def render_report(data):
    rows = data["results"]
    lines = ["# Generated CPLcofold / LinearCoFold comparisons", "", f"Run: {data['run_utc']}", "",
             f"Run scope: {data['scope']}; {len(data['selected_case_ids'])} pairs, {len(rows)} comparisons.", "",
             f"Validated score/energy agreement: **{'PASS' if data['passed'] else 'FAIL'}**. "
             "Different structures with equal, independently validated energy are reported separately as `equal_energy_tie`.", "",
             "Settings: alpha=0, beta=0, no pseudoknots, max_phase1=1, energy_delta=0. "
             "Python receives the exact strand order printed by C++. Every prediction and reference is checked for finite energy, "
             "valid strand boundary and balanced structure, then independently rescored. The Python search score must also match physical energy.", "",
             "Synthetic sequences test software behavior. The two optional real windows have pinned sequence provenance; "
             "neither comparison establishes accuracy against native RNA structures or measured binding free energy. "
             "Beam 0 disables pruning but retains the ordinary 30-nt internal-loop limit. Finite beams prune different state families; "
             "any energy differences remain visible and cause a failed agreement verdict. No timing claim is made.", "",
             "| Beam | Identical | Equal-energy tie | Energy difference | Scoring mismatch | Other error |",
             "|---:|---:|---:|---:|---:|---:|"]
    for beam in BEAMS:
        counts = Counter(row["status"] for row in rows if row["beam"] == beam)
        other = sum(value for key, value in counts.items() if key not in {"identical", "equal_energy_tie", "energy_difference", "scoring_mismatch"})
        lines.append(f"| {beam} | {counts['identical']} | {counts['equal_energy_tie']} | {counts['energy_difference']} | {counts['scoring_mismatch']} | {other} |")
    lines += ["", "| Case | Compared lengths | Beam | Python energy | Reference energy | Pair differences | Outcome |",
              "|---|---|---:|---:|---:|---:|---|"]
    for row in rows:
        display = lambda key: f"{row[key]:.2f}" if key in row else "—"
        lines.append(f"| {row['case_id']} | {'+'.join(map(str, row['lengths']))} | {row['beam']} | "
                     f"{display('energy')} | {display('reference_energy')} | {row.get('pair_symmetric_difference', '—')} | {row['status']} |")
    lines += ["", "## Execution checks", ""]
    lines += ["- " + failure for failure in data["failures"]] or ["No execution or scoring failures."]
    lines += ["", "Full input sequences, all predictions, raw reference output, source/compiler hashes and the executed script "
              "are preserved beside this report. A pilot does not stand in for the complete panel.", "", "## Reproduce", "", "```sh",
              data["reproduce_command"], "```", ""]
    return "\n".join(lines)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=ROOT.parent / "VincentZhangHe-cofold-copartition-13c0beee5b11")
    parser.add_argument("--output", type=Path, default=ROOT / "artifacts/cplcofold-generated")
    parser.add_argument("--fixtures", type=Path, help="Replay a generated fixtures.json instead of generating new sequences")
    parser.add_argument("--real-fixtures", type=Path, default=ROOT / "artifacts/cplcofold-local-benchmark/fixtures.json")
    parser.add_argument("--no-real", action="store_true")
    parser.add_argument("--seed", type=int, default=20261027)
    parser.add_argument("--synthetic-count", type=int, default=80)
    parser.add_argument("--pilot", type=int, default=0, help="Select this many evenly spaced pairs; zero runs the full panel")
    parser.add_argument("--compiler", default="g++")
    parser.add_argument("--timeout", type=float, default=1800.0)
    args = parser.parse_args()
    if not 60 <= args.synthetic_count <= 100 or args.pilot < 0 or not math.isfinite(args.timeout) or args.timeout <= 0:
        parser.error("Require 60–100 synthetic pairs, nonnegative pilot size and finite positive timeout")
    helper = comparison_module()
    files = {"executed-runner.py": SCRIPT.read_text(), "comparison-helper.py": HELPER.read_text()}
    try:
        # Read replay fixtures before refreshing the same output directory.
        fixture_text = args.fixtures.read_text() if args.fixtures else json_text(generate_fixtures(
            args.seed, args.synthetic_count, None if args.no_real else args.real_fixtures))
        fixtures = strict_loads(fixture_text)
        helper.validate_fixtures(fixtures)
        for case in fixtures["cases"]:
            if not isinstance(case.get("category"), str) or len(case["sequence_a"] + case["sequence_b"]) > 140:
                raise ValueError("Each fixture requires a category and at most 140 total nucleotides")
        selected = select_cases(fixtures["cases"], args.pilot)
        files["fixtures.json"] = fixture_text
        files["results.json"] = json_text(dict(passed=False, status="running"))
        files["report.md"] = "# Generated CPLcofold comparisons\n\nRUNNING: no completed agreement verdict.\n"
        helper.write_exports(args.output, files)
        before = source_manifest()
        reference = args.reference.resolve()
        reference_before = helper.manifest(reference)
        if not reference_before:
            raise ValueError("No reference source files found")
        references = []
        with tempfile.TemporaryDirectory(prefix="cplcofold-generated-", dir="/tmp") as directory:
            temporary = Path(directory)
            binary = temporary / "linearcofold_v"
            command = [args.compiler, str(reference / "src/LinearCoFold.cpp"), *helper.FLAGS, "-o", str(binary)]
            print(f"Compiling reference into /tmp; selected {len(selected)}/{len(fixtures['cases'])} pairs, beams {BEAMS}.", flush=True)
            compiled = subprocess.run(command, capture_output=True, text=True, check=True, timeout=args.timeout)
            binary_sha = helper.sha(binary)
            for case in selected:
                for beam in BEAMS:
                    listed = case["sequence_a"] + "&" + case["sequence_b"]
                    run = None
                    try:
                        run = subprocess.run([str(binary), str(beam), "0", "0"], input=listed + "\n",
                                             capture_output=True, text=True, timeout=args.timeout)
                        row = parse_reference(case, beam, run, helper)
                    except Exception as error:
                        def decoded(value):
                            return value.decode(errors="replace") if isinstance(value, bytes) else value or ""
                        row = dict(case_id=case["id"], beam=beam, category=case["category"], listed_sequence=listed,
                                   lengths=[len(case["sequence_a"]), len(case["sequence_b"])],
                                   status="reference_error", comparison_ok=False,
                                   reference_stdout=run.stdout if run is not None else decoded(getattr(error, "stdout", None)),
                                   reference_stderr=run.stderr if run is not None else decoded(getattr(error, "stderr", None)),
                                   error=dict(type=type(error).__name__, message=str(error)))
                    references.append(row)
                    if len(references) % 20 == 0:
                        print(f"Reference {len(references)}/{len(selected) * len(BEAMS)} complete.", flush=True)
            files["reference-outputs.json"] = json_text(references)
            config, worker_output = temporary / "worker-config.json", temporary / "worker-results.json"
            config.write_text(json_text(dict(rows=references)))
            environment = os.environ.copy()
            for key in ("NUMBA_DISABLE_JIT", "PYTHONPATH", "PYTHONHOME"):
                environment.pop(key, None)
            print("Running one Python worker with real Numba; existing module caches are allowed.", flush=True)
            completed = subprocess.run([sys.executable, "-I", "-B", str(SCRIPT), "--worker", str(config), str(worker_output)],
                                       env=environment, cwd=temporary, timeout=args.timeout, check=False)
            if completed.returncode != 0:
                raise RuntimeError(f"Python worker exited with status {completed.returncode}")
            payload = strict_loads(worker_output.read_text())
            files["python-worker.json"] = json_text(payload)
        rows, failures = payload["results"], []
        expected = [(case["id"], beam) for case in selected for beam in BEAMS]
        if [(row["case_id"], row["beam"]) for row in rows] != expected:
            raise ValueError("Worker omitted/reordered a case or beam")
        for name in ("_inside", "_face_energies", "_traceback_best"):
            if not payload["nopython_signatures"].get(name):
                failures.append("Missing real nopython signature: " + name)
        if payload["numba_disabled"] or Path(payload["parser_module"]).resolve() != (ROOT / "third_party/cplfold/CPLcofold_parser.py").resolve():
            failures.append("Unexpected Python parser or disabled JIT")
        if source_manifest() != before or helper.manifest(reference) != reference_before:
            failures.append("Python/helper/reference source changed during the run")
        for row in rows:
            if not row["comparison_ok"]:
                failures.append(f"{row['case_id']} beam={row['beam']}: {row['status']}")
        reproduce = [sys.executable, str(SCRIPT), "--reference", str(reference), "--fixtures", str((args.output / "fixtures.json").resolve()),
                     "--output", str(args.output.resolve()), "--pilot", str(args.pilot), "--compiler", args.compiler, "--timeout", str(args.timeout)]
        data = dict(schema_version=1, run_utc=datetime.now(timezone.utc).isoformat(),
                    status="passed" if not failures else "failed", passed=not failures,
                    scope="pilot" if len(selected) < len(fixtures["cases"]) else "full_panel",
                    selected_case_ids=[case["id"] for case in selected], fixtures=fixtures,
                    fixture_sha256=hashlib.sha256(fixture_text.encode()).hexdigest(), parameters=PARAMETERS, beams=list(BEAMS),
                    reference=str(reference), reference_source_sha256=reference_before, reference_binary_sha256=binary_sha,
                    compiler_flags=helper.FLAGS, compiler_stderr=compiled.stderr,
                    compiler_version=subprocess.run([args.compiler, "--version"], capture_output=True, text=True, check=True).stdout.splitlines()[0],
                    cplfold_source_sha256=before, comparison_script_sha256=helper.sha(SCRIPT), platform=platform.platform(),
                    python=payload["python"], numpy=payload["numpy"], numba=payload["numba"],
                    nopython_signatures=payload["nopython_signatures"], results=rows, failures=failures,
                    reproduce_command=shlex.join(reproduce))
        files.update({"results.json": json_text(data), "report.md": render_report(data)})
        helper.write_exports(args.output, files)
        print(f"{data['status'].upper()}: {dict(Counter(row['status'] for row in rows))}. Report: {args.output / 'report.md'}", flush=True)
        return 0 if data["passed"] else 1
    except Exception as error:
        failure = dict(passed=False, status="failed", error=dict(type=type(error).__name__, message=str(error), traceback=traceback.format_exc()))
        files.update({"failure.json": json_text(failure), "results.json": json_text(failure),
                      "report.md": "# Generated CPLcofold comparisons\n\nFAIL: " + str(error) + "\n"})
        helper.write_exports(args.output, files)
        raise


if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--worker":
        worker(Path(sys.argv[2]), Path(sys.argv[3]))
    else:
        raise SystemExit(main())
