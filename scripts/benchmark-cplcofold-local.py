#!/usr/bin/env python3
"""Reproducible local-RNA correctness and timing comparison with LinearCoFold."""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shlex
import statistics
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
SCRIPT = Path(__file__).resolve()
OUTPUT = ROOT / "artifacts" / "cplcofold-local-benchmark"
PARAMETERS = dict(alpha=0.0, beta=0.0, allow_pseudoknot=False, energy_delta=0.0, max_phase1=1)


def comparison_module():
    spec = importlib.util.spec_from_file_location("cofold_comparison", ROOT / "scripts/compare-cplcofold-linearcofold.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def timed_run(command, *, input_text=None, environment=None, timeout=600):
    start = time.perf_counter_ns()
    completed = subprocess.run(command, input=input_text, env=environment, capture_output=True,
                               text=True, check=True, timeout=timeout, cwd=ROOT)
    return completed, (time.perf_counter_ns() - start) / 1e9


def distribution(samples):
    if not samples or any(not (0 < value < float("inf")) for value in samples):
        raise ValueError("Timings must be nonempty and finite positive seconds")
    quartiles = statistics.quantiles(samples, n=4, method="inclusive") if len(samples) > 1 else samples * 3
    return dict(n=len(samples), median_s=statistics.median(samples), p25_s=quartiles[0],
                p75_s=quartiles[2], min_s=min(samples), max_s=max(samples), samples_s=samples)


def native_result(stdout):
    sequences = re.findall(r"^([ACGU]+&[ACGU]+)$", stdout, re.MULTILINE)
    matches = re.findall(r"^([.()]+&[.()]+)\s+\((-?\d+\.\d+)\)$", stdout, re.MULTILINE)
    if len(sequences) != 1 or len(matches) != 1:
        raise ValueError("Unexpected original LinearCoFold output")
    return sequences[0], matches[0][0], float(matches[0][1])


def check_signature(prediction, row):
    if (prediction["structure"] != row["reference_structure"]
            or not abs(prediction["energy"] - row["reference_energy"]) < 1e-7):
        raise ValueError("A timed prediction differs from the verified reference")


def worker():
    config = json.load(sys.stdin)
    start = time.perf_counter_ns()
    sys.path.insert(0, str(ROOT / "third_party/cplfold"))
    from CPLfold import cofold
    from Utils.cofold_energy import CofoldEnergy
    import CPLcofold_parser
    import numba
    import numpy
    import_seconds = (time.perf_counter_ns() - start) / 1e9
    if numba.config.DISABLE_JIT:
        raise RuntimeError("This benchmark requires actual Numba JIT")
    comparator = comparison_module()

    def predict(row):
        a, b = row["compared_sequence"].split("&")
        return cofold(a, b, beam_size=row["beam"], verbose=False, **PARAMETERS)[0]

    # First folding call in this interpreter; the parent controls cache state.
    first = config["rows"][0]
    start = time.perf_counter_ns()
    first_result = predict(first)
    first_call_seconds = (time.perf_counter_ns() - start) / 1e9
    check_signature(first_result, first)
    evaluator = CofoldEnergy()
    measured = []
    for row in config["rows"]:
        for _ in range(config["warmups"]):
            check_signature(predict(row), row)
        samples = []
        result = first_result
        for _ in range(config["repeats"]):
            start = time.perf_counter_ns()
            result = predict(row)
            samples.append((time.perf_counter_ns() - start) / 1e9)
            check_signature(result, row)  # Validation is outside the timer.
        sequence = row["compared_sequence"]
        comparison = comparator.compare_prediction(sequence, result, row["reference_structure"],
            row["reference_energy"],
            evaluator.compute_energy(sequence, row["reference_structure"], sequence.index("&"))["energy"],
            evaluator.compute_energy(sequence, result["structure"], sequence.index("&"))["energy"])
        if comparison["status"] != "identical":
            raise ValueError("Independent rescoring did not validate an identical prediction")
        measured.append(dict(case_id=row["case_id"], beam=row["beam"], comparison=comparison,
                             warm_api=distribution(samples) if samples else None))
    compiled = {name: len(value.nopython_signatures) for name, value in vars(CPLcofold_parser).items()
                if hasattr(value, "nopython_signatures") and value.nopython_signatures}
    if not compiled:
        raise RuntimeError("No parser kernel compiled in nopython mode")
    print(json.dumps(dict(import_s=import_seconds, first_call_s=first_call_seconds,
        numba=numba.__version__, numpy=numpy.__version__, compiled=compiled, results=measured), allow_nan=False))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--fixtures", type=Path, default=OUTPUT / "fixtures.json")
    parser.add_argument("--output", type=Path, default=OUTPUT)
    parser.add_argument("--reference", type=Path, default=ROOT.parent / "VincentZhangHe-cofold-copartition-13c0beee5b11")
    parser.add_argument("--compiler", default="g++")
    parser.add_argument("--repeats", type=int, default=51)
    parser.add_argument("--process-repeats", type=int, default=7)
    parser.add_argument("--warmups", type=int, default=3)
    parser.add_argument("--skip-cold", action="store_true",
                        help="Skip cold timing; share an explicit inherited or temporary Numba cache")
    args = parser.parse_args()
    if min(args.repeats, args.process_repeats, args.warmups) < 1:
        parser.error("Repeat and warmup counts must be positive")
    comparator = comparison_module()
    fixture_bytes = args.fixtures.read_bytes()
    fixtures = json.loads(fixture_bytes)
    comparator.validate_fixtures(fixtures)
    reference = args.reference.resolve()
    source_before = comparator.manifest(reference)
    harness = ROOT / "scripts/linearcofold-benchmark.cpp"
    rows = []
    with tempfile.TemporaryDirectory(prefix="cofold-local-timing-", dir="/tmp") as directory:
        temporary = Path(directory)
        binary, core_binary = temporary / "original", temporary / "core-timer"
        compiler_info = subprocess.run([args.compiler, "--version"], capture_output=True, text=True, check=True).stdout.splitlines()[0]
        compile_original, compile_original_s = timed_run([args.compiler,
            str(reference / "src/LinearCoFold.cpp"), *comparator.FLAGS, "-o", str(binary)])
        compile_harness, compile_harness_s = timed_run([args.compiler, str(harness), *comparator.FLAGS,
            '-DLINEARCOFOLD_SOURCE="' + str(reference / "src/LinearCoFold.cpp") + '"', "-o", str(core_binary)])
        print("Compiled unchanged reference and quiet timing harness in /tmp.", flush=True)
        for case in fixtures["cases"]:
            listed = case["sequence_a"] + "&" + case["sequence_b"]
            for beam in (100, 0):
                baseline, _ = timed_run([str(binary), str(beam), "0", "0"], input_text=listed + "\n")
                actual, structure, energy = native_result(baseline.stdout)
                comparator.compared_order(case, actual)
                comparator.validate_structure(actual, structure)
                rows.append(dict(case_id=case["id"], beam=beam, listed_sequence=listed,
                    compared_sequence=actual, reference_structure=structure, reference_energy=energy,
                    reference_swapped_strands=actual != listed, lengths=[len(s) for s in actual.split("&")],
                    biological_provenance_verified=case["biological_provenance_verified"],
                    sources=case["sources"], reference_stdout=baseline.stdout))
        environment = os.environ.copy()
        environment.pop("NUMBA_DISABLE_JIT", None)
        environment["PYTHONDONTWRITEBYTECODE"] = "1"
        cold = None
        if args.skip_cold:
            if environment.get("NUMBA_CACHE_DIR"):
                cache_policy = "inherited_explicit"
                print("Skipping cold-start measurement; worker and CLI share the inherited NUMBA_CACHE_DIR.", flush=True)
            else:
                cache = temporary / "shared-numba-cache"
                cache.mkdir()
                environment["NUMBA_CACHE_DIR"] = str(cache)
                cache_policy = "shared_temporary"
                print("Skipping cold-start measurement; worker and CLI share a temporary Numba cache populated before measured calls.", flush=True)
        else:
            cache_policy = "empty_then_populated"
            environment["NUMBA_CACHE_DIR"] = str(temporary / "empty-numba-cache")
            cache = Path(environment["NUMBA_CACHE_DIR"])
            cache.mkdir()
            if any(cache.iterdir()):
                raise RuntimeError("Cold timing requires an empty Numba cache")
            print("Timing the first public API call with an empty Numba cache...", flush=True)
            cold_process, cold_wall = timed_run([sys.executable, str(SCRIPT), "--worker"], environment=environment,
                input_text=json.dumps(dict(rows=rows[:1], warmups=0, repeats=0)))
            cold = json.loads(cold_process.stdout)
            cold["process_wall_s"] = cold_wall
            cold["case_id"], cold["beam"] = rows[0]["case_id"], rows[0]["beam"]
            print(f"Empty-cache first call: {cold['first_call_s']:.3f} s; process wall: {cold_wall:.3f} s.", flush=True)
        print("Measuring warmed public API calls sequentially.", flush=True)
        warm_process, warm_wall = timed_run([sys.executable, str(SCRIPT), "--worker"], environment=environment,
            input_text=json.dumps(dict(rows=rows, warmups=args.warmups, repeats=args.repeats)))
        warm = json.loads(warm_process.stdout)
        if len(warm["results"]) != len(rows):
            raise ValueError("Worker result count changed")
        for row, measured in zip(rows, warm["results"]):
            if (row["case_id"], row["beam"]) != (measured["case_id"], measured["beam"]):
                raise ValueError("Worker result order changed")
            row.update(measured["comparison"])
            row["cplcofold_warm_api"] = measured["warm_api"]
            core_run, _ = timed_run([str(core_binary), str(row["beam"]), str(args.repeats), str(args.warmups)],
                                    input_text=row["compared_sequence"] + "\n")
            core_lines = core_run.stdout.strip().splitlines()
            if not core_lines or core_lines[0] != "elapsed_ns\tstructure\tenergy_kcal_mol":
                raise ValueError("Unexpected C++ timing harness output")
            core_samples = []
            for line in core_lines[1:]:
                elapsed, structure, energy = line.split("\t")
                check_signature(dict(structure=structure, energy=float(energy)), row)
                core_samples.append(int(elapsed) / 1e9)
            if len(core_samples) != args.repeats:
                raise ValueError("C++ harness returned an unexpected sample count")
            row["linearcofold_warm_parser"] = distribution(core_samples)
            # Actual CLIs with populated disk cache; alternate launch order.
            python_cli = [sys.executable, str(ROOT / "bin/cplfold"), "--sequence", row["compared_sequence"],
                "--beam", str(row["beam"]), "--alpha", "0", "--beta", "0", "--delta", "0",
                "--max-phase1", "1", "--no-pseudoknot"]
            cpp_cli = [str(binary), str(row["beam"]), "0", "0"]
            samples = {"python": [], "cpp": []}
            for repeat in range(args.process_repeats):
                for kind in (("python", "cpp") if repeat % 2 == 0 else ("cpp", "python")):
                    if kind == "python":
                        run, wall = timed_run(python_cli, environment=environment)
                        match = re.search(r"^S-1: ([.()]+&[.()]+)\n\s+Energy: (-?\d+\.\d+) kcal/mol", run.stdout, re.MULTILINE)
                        if not match:
                            raise ValueError("CPLcofold CLI did not print a prediction")
                        check_signature(dict(structure=match.group(1), energy=float(match.group(2))), row)
                    else:
                        run, wall = timed_run(cpp_cli, input_text=row["compared_sequence"] + "\n")
                        actual, structure, energy = native_result(run.stdout)
                        if actual != row["compared_sequence"]:
                            raise ValueError("Reference sequence order changed")
                        check_signature(dict(structure=structure, energy=energy), row)
                    samples[kind].append(wall)
            row["cplcofold_cached_cli"] = distribution(samples["python"])
            row["linearcofold_cli"] = distribution(samples["cpp"])
            row["warm_latency_ratio_cpl_over_cpp"] = row["cplcofold_warm_api"]["median_s"] / row["linearcofold_warm_parser"]["median_s"]
            row["cli_latency_ratio_cpl_over_cpp"] = row["cplcofold_cached_cli"]["median_s"] / row["linearcofold_cli"]["median_s"]
            print(f"{row['case_id']} beam={row['beam']}: identical; warm CPL {row['cplcofold_warm_api']['median_s'] * 1000:.3f} ms, "
                  f"C++ {row['linearcofold_warm_parser']['median_s'] * 1000:.3f} ms; ratio {row['warm_latency_ratio_cpl_over_cpp']:.2f}x", flush=True)
        if comparator.manifest(reference) != source_before:
            raise RuntimeError("Reference source changed while benchmarking")
        data = dict(run_utc=datetime.now(timezone.utc).isoformat(), fixtures=fixtures,
            fixture_sha256=hashlib.sha256(fixture_bytes).hexdigest(), parameters=PARAMETERS,
            repeats=args.repeats, process_repeats=args.process_repeats, warmups=args.warmups,
            numba_disabled=False, python=sys.version, python_executable=sys.executable, platform=platform.platform(),
            machine=platform.machine(), cpu_count=os.cpu_count(), numba=warm["numba"], numpy=warm["numpy"],
            nopython_signatures=warm["compiled"], reference=str(reference), compiler=compiler_info,
            compiler_flags=comparator.FLAGS, cpp_compile_s=compile_original_s, harness_compile_s=compile_harness_s,
            compiler_stderr=compile_original.stderr + compile_harness.stderr,
            reference_source_sha256=source_before, reference_binary_sha256=comparator.sha(binary),
            harness_binary_sha256=comparator.sha(core_binary),
            benchmark_source_sha256={str(path.relative_to(ROOT)): comparator.sha(path) for path in
                [SCRIPT, harness, ROOT / "scripts/compare-cplcofold-linearcofold.py", ROOT / "bin/cplfold"]},
            cplfold_source_sha256={str(path.relative_to(ROOT)): comparator.sha(path) for path in
                sorted((ROOT / "third_party/cplfold").rglob("*.py")) if "tests" not in path.parts},
            cold_start=cold, skip_cold=args.skip_cold,
            numba_cache_policy=cache_policy,
            cached_worker_import_s=warm["import_s"], cached_worker_first_call_s=warm["first_call_s"],
            warm_worker_wall_s=warm_wall, results=rows)
    files = comparator.build_exports(data)
    files.update({"fixtures.json": fixture_bytes.decode("utf-8"), "results.json": comparator.json_text(data),
                  "report.md": render_report(data, args), "linearcofold-benchmark.cpp": harness.read_text()})
    comparator.write_exports(args.output, files)
    print(f"Wrote {args.output / 'report.md'}", flush=True)


def render_report(data, args):
    cold, rows = data["cold_start"], data["results"]
    report = ["# CPLcofold / LinearCoFold local RNA timing", "", f"Run: {data['run_utc']}", "",
        "Alpha=0, beta=0, no pseudoknots, energy_delta=0, max_phase1=1. All timed predictions must match the original C++ structure and energy, "
        "and both structures are independently rescored outside the timer.", "",
        "| Local RNA pair | Lengths | Beam | Energy (kcal/mol), both | CPLcofold warm API (ms) | C++ warm parser (ms) | CPL/C++ time ratio |",
        "|---|---|---:|---:|---:|---:|---:|"]
    for row in rows:
        report.append(f"| {row['case_id']} | {'+'.join(map(str,row['lengths']))} | {row['beam']} | {row['energy']:.2f} | "
            f"{row['cplcofold_warm_api']['median_s']*1000:.3f} | {row['linearcofold_warm_parser']['median_s']*1000:.3f} | {row['warm_latency_ratio_cpl_over_cpp']:.2f}x |")
    report += ["", "Ratios greater than 1 mean CPLcofold is slower. All rows are `identical`, including independent rescoring.", "",
        "## Timing scope", "",
        f"Warm timings are medians of {data['repeats']} calls after {data['warmups']} discarded warmups per setting. "
        "Python times the complete public API (search, traceback, independent energy evaluation, metadata and report formatting). "
        "C++ times construction, quiet parsing/traceback and destruction of the original parser via the included thin harness. "
        "The original source is included unchanged, with its main function renamed; only an outer monotonic timer is added. "
        "Result checking and output occur outside these timers. These are usable API/parser latencies, not an isolated-kernel comparison.", "",
        "Fresh-process timings below use the actual bin/cplfold CLI with a populated Numba disk cache and the unmodified original C++ executable. "
        f"Each is the median of {data['process_repeats']} launches, alternating Python/C++ order. They include interpreter/import/cache-load costs and output. "
        "Compilation of the C++ executable is excluded and recorded separately. All benchmark processes run sequentially; the host is shared and uncontrolled. "
        "Raw samples and quartiles are retained in results.json. These two windows from the same RNAs do not establish performance on other lengths or datasets. "
        "Beam=100 may not prune any states for short inputs; it must not be interpreted as a scaling benchmark.", "",
        "| Local RNA pair | Beam | CPLcofold cached CLI (ms) | C++ CLI (ms) | CPL/C++ time ratio |",
        "|---|---:|---:|---:|---:|"]
    for row in rows:
        report.append(f"| {row['case_id']} | {row['beam']} | {row['cplcofold_cached_cli']['median_s']*1000:.3f} | "
            f"{row['linearcofold_cli']['median_s']*1000:.3f} | {row['cli_latency_ratio_cpl_over_cpp']:.2f}x |")
    if cold is None:
        cache_note = ("The worker and CLI shared the inherited explicit `NUMBA_CACHE_DIR`. "
                      if data["numba_cache_policy"] == "inherited_explicit" else
                      "The worker and CLI shared a temporary `NUMBA_CACHE_DIR`, populated before measured calls. ")
        report += ["", "Cold-start measurement was skipped (`--skip-cold`). " + cache_note +
            "The worker's first call may load cached code or compile missing signatures and is excluded from warmed samples. "
            "No empty-cache latency was measured."]
    else:
        report += ["", f"Empty-cache first API call ({cold['case_id']}, beam={cold['beam']}): {cold['first_call_s']:.3f} s. "
            f"Its worker import time is {cold['import_s']:.3f} s and total subprocess wall time is {cold['process_wall_s']:.3f} s. "
            "This one observation includes JIT compilation and is not a warmed fold latency. "
            f"A fresh worker using that disk cache had first-call latency {data['cached_worker_first_call_s']:.3f} s."]
    command = [sys.executable, str(SCRIPT), "--fixtures", str((args.output / "fixtures.json").resolve()),
        "--output", str(args.output.resolve()), "--reference", data["reference"], "--compiler", args.compiler,
        "--repeats", str(args.repeats), "--process-repeats", str(args.process_repeats), "--warmups", str(args.warmups)]
    if data.get("skip_cold", cold is None):
        command.append("--skip-cold")
    report += ["",
        "## Inputs and reproduction", "",
        "Coordinates are 1-based inclusive positions in the deposited FASTA. Windows are illustrative; excluded flanks and proteins are not modelled. "
        "Agreement checks software implementation, not biological accuracy. Fixture provenance and full sequences, mapped pair TSVs and per-beam summaries are included.", "",
        "```sh", shlex.join(command), "```", ""]
    return "\n".join(report)


if __name__ == "__main__":
    if "--worker" in sys.argv[1:]:
        worker()
    else:
        main()
