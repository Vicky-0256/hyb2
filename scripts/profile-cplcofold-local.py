#!/usr/bin/env python3
"""Measure warmed CPLcofold phases without changing the folding implementation."""
from __future__ import annotations

import argparse
import cProfile
from datetime import datetime, timezone
import importlib.util
import json
import math
from pathlib import Path
import platform
import pstats
import statistics
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "artifacts/cplcofold-local-profile"


def load_script(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "scripts" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def subphase_distribution(samples):
    # Very short native constructors can fall below the clock's resolution.
    # Keep measured zero durations rather than replacing them with invented time.
    if not samples or any(not math.isfinite(value) or value < 0 for value in samples):
        raise ValueError("Subphase durations must be finite and nonnegative")
    quartiles = statistics.quantiles(samples, n=4, method="inclusive") if len(samples) > 1 else samples * 3
    return dict(n=len(samples), median_s=statistics.median(samples), p25_s=quartiles[0], p75_s=quartiles[2],
                min_s=min(samples), max_s=max(samples), samples_s=samples)


def main():
    argument_parser = argparse.ArgumentParser(description=__doc__)
    argument_parser.add_argument("--baseline", type=Path, default=ROOT / "artifacts/cplcofold-local-benchmark/results.json")
    argument_parser.add_argument("--output", type=Path, default=OUTPUT)
    argument_parser.add_argument("--repeats", type=int, default=51)
    argument_parser.add_argument("--profile-repeats", type=int, default=3)
    argument_parser.add_argument("--compiler", default="g++")
    args = argument_parser.parse_args()
    if min(args.repeats, args.profile_repeats) < 1:
        argument_parser.error("Repeat counts must be positive")
    benchmark = load_script("timing_helpers", "benchmark-cplcofold-local.py")
    comparator = benchmark.comparison_module()
    baseline = json.loads(args.baseline.read_text())
    reference = Path(baseline["reference"])
    if comparator.manifest(reference) != baseline["reference_source_sha256"]:
        raise ValueError("Reference source differs from the validated benchmark")
    for name, digest in baseline["cplfold_source_sha256"].items():
        if comparator.sha(ROOT / name) != digest:
            raise ValueError("CPLfold source differs from the validated benchmark")
    sys.path.insert(0, str(ROOT / "third_party/cplfold"))
    import CPLfold as api
    import CPLcofold_parser as parser_module
    import Utils.cofold_energy as energy_module
    from Utils.cofold_energy import CofoldEnergy
    import numba
    import numpy as np
    if numba.config.DISABLE_JIT:
        raise RuntimeError("The profile requires actual Numba JIT")
    fast_traceback = hasattr(parser_module._Forest, "best")

    def predict(row):
        a, b = row["compared_sequence"].split("&")
        return api.cofold(a, b, beam_size=row["beam"], verbose=False, **benchmark.PARAMETERS)[0]

    def check(result, row):
        benchmark.check_signature(result, row)

    def uninstrumented(row):
        samples = []
        for _ in range(args.repeats):
            start = time.perf_counter_ns()
            result = predict(row)
            samples.append((time.perf_counter_ns() - start) / 1e9)
            check(result, row)
        return benchmark.distribution(samples)

    def phase_profile(row):
        # Forest time includes inside and face time. Other measured categories
        # do not nest; exclusive preparation/API overhead can be subtracted.
        trace_target = ((parser_module._Forest, "best", "traceback") if fast_traceback
                        else (parser_module._KBest, "get", "traceback"))
        targets = [(parser_module, "_inside", "inside"), (parser_module, "_face_energies", "faces"),
            (parser_module._Forest, "__init__", "forest"), trace_target,
            (CofoldEnergy, "compute_energy", "energy"), (api, "output_results", "formatting")]
        originals, all_samples = [], []
        current = {}

        def wrap(original, label):
            def measured(*positional, **keywords):
                start = time.perf_counter_ns()
                try:
                    return original(*positional, **keywords)
                finally:
                    current[label] = current.get(label, 0) + time.perf_counter_ns() - start
            return measured

        try:
            for owner, name, label in targets:
                original = getattr(owner, name)
                originals.append((owner, name, original))
                setattr(owner, name, wrap(original, label))
            for _ in range(args.repeats):
                current.clear()
                start = time.perf_counter_ns()
                result = predict(row)
                total = time.perf_counter_ns() - start
                check(result, row)
                exclusive = dict(inside=current["inside"], faces=current["faces"],
                    preparation=current["forest"] - current["inside"] - current["faces"],
                    traceback=current["traceback"], energy=current["energy"], formatting=current["formatting"],
                    api_other=total - sum(current[key] for key in ("forest", "traceback", "energy", "formatting")))
                if min(exclusive.values()) < 0 or sum(exclusive.values()) != total:
                    raise ValueError("Phase times do not partition the measured API call")
                all_samples.append({key: value / 1e9 for key, value in dict(exclusive, total=total).items()})
        finally:
            for owner, name, original in reversed(originals):
                setattr(owner, name, original)
        total_seconds = sum(sample["total"] for sample in all_samples)
        statistics = {key: benchmark.distribution([sample[key] for sample in all_samples]) for key in all_samples[0]}
        shares = {key: sum(sample[key] for sample in all_samples) / total_seconds for key in all_samples[0] if key != "total"}
        return dict(statistics=statistics, aggregate_time_fraction=shares, samples=all_samples)

    def call_profile(row):
        profiler = cProfile.Profile()
        for _ in range(args.profile_repeats):
            check(profiler.runcall(predict, row), row)
        entries = []
        for (filename, line, name), (primitive, total, own, cumulative, _) in pstats.Stats(profiler).stats.items():
            entries.append(dict(file=filename, line=line, name=name, primitive_calls=primitive,
                total_calls=total, own_s=own, cumulative_s=cumulative,
                calls_per_prediction=total / args.profile_repeats))
        return dict(predictions=args.profile_repeats, top_self=sorted(entries, key=lambda item: item["own_s"], reverse=True)[:25],
            top_cumulative=sorted(entries, key=lambda item: item["cumulative_s"], reverse=True)[:25],
            note="cProfile overhead changes latency, especially Python code. Use these rows to locate calls, not as uninstrumented timing shares.")

    def chart_diagnostics(row):
        sequence = row["compared_sequence"].replace("&", "")
        cutpoint = row["compared_sequence"].index("&")
        parser = parser_module.BeamCKYCofoldParser(cutpoint=cutpoint, beam_size=row["beam"])
        forest = parser._prepare(sequence, None)
        unpruned = parser_module.BeamCKYCofoldParser(cutpoint=cutpoint, beam_size=0)._prepare(sequence, None)
        kbest = parser_module._KBest(forest)
        score, pairs = kbest.get((parser_module._C, 0, len(sequence)), 0)
        if not abs(score / 100.0 - row["reference_energy"]) < 1e-7:
            raise ValueError("Diagnostic traceback energy changed")
        if sorted(pairs) != comparator.validate_structure(row["compared_sequence"], row["reference_structure"]):
            raise ValueError("Diagnostic traceback pairs changed")
        finite = forest.pair < parser_module._INF / 2
        return dict(n=len(sequence), finite_pair_states=int(np.count_nonzero(finite)),
            maximum_pairs_per_column=int(np.count_nonzero(finite, axis=0).max()),
            pair_chart_equals_beam0=bool(np.array_equal(forest.pair, unpruned.pair)),
            m1_chart_equals_beam0=bool(np.array_equal(forest.m1, unpruned.m1)),
            m2_chart_equals_beam0=bool(np.array_equal(forest.m2, unpruned.m2)),
            finite_m1_states=int(np.count_nonzero(forest.m1 < parser_module._INF / 2)),
            finite_m2_states=int(np.count_nonzero(forest.m2 < parser_module._INF / 2)),
            legacy_oracle_records=len(kbest.records),
            legacy_oracle_retained_edges=sum(len(record["edges"]) for record in kbest.records.values()),
            emitted_base_pairs=len(pairs))

    harness = ROOT / "scripts/linearcofold-profile.cpp"
    results = []
    diagnostic_charts = {}
    print("Warming actual Numba functions; all warmup and cache-load time is excluded.", flush=True)
    for row in baseline["results"]:
        for _ in range(3):
            check(predict(row), row)
        # The legacy oracle calls scalar kernels from Python that the fast
        # path calls only inside JIT code. Warm those separate dispatchers
        # before recording signatures; diagnostics are never timed work.
        diagnostic_charts[(row["case_id"], row["beam"])] = chart_diagnostics(row)
    def compiled_signatures():
        return {label + "." + name: str(value.nopython_signatures)
                for label, module in (("parser", parser_module), ("energy", energy_module))
                for name, value in vars(module).items()
                if hasattr(value, "nopython_signatures") and value.nopython_signatures}

    signatures_before = compiled_signatures()
    if not signatures_before:
        raise ValueError("No nopython kernel signature found")
    with tempfile.TemporaryDirectory(prefix="cofold-local-profile-", dir="/tmp") as directory:
        binary = Path(directory) / "native-profile"
        command = [args.compiler, str(harness), *comparator.FLAGS,
            '-DLINEARCOFOLD_SOURCE="' + str(reference / "src/LinearCoFold.cpp") + '"', "-o", str(binary)]
        build = subprocess.run(command, text=True, capture_output=True, check=True, timeout=300)
        for row in baseline["results"]:
            before = uninstrumented(row)
            phases = phase_profile(row)
            after = uninstrumented(row)
            # Call-count profiling runs separately from the low-overhead timers.
            profile = call_profile(row)
            diagnostics = diagnostic_charts[(row["case_id"], row["beam"])]
            run = subprocess.run([str(binary), str(row["beam"]), str(args.repeats), "3"],
                input=row["compared_sequence"] + "\n", text=True, capture_output=True, check=True, timeout=300)
            lines = run.stdout.strip().splitlines()
            if not lines or lines[0] != "constructor_ns\tparse_ns\tdestruction_ns\tstructure\tenergy_kcal_mol":
                raise ValueError("Unexpected native profiling output")
            native_samples = []
            for line in lines[1:]:
                constructor, parse, destruction, structure, energy = line.split("\t")
                check(dict(structure=structure, energy=float(energy)), row)
                values = dict(constructor=int(constructor) / 1e9, parse=int(parse) / 1e9, destruction=int(destruction) / 1e9)
                values["total"] = sum(values.values())
                native_samples.append(values)
            if len(native_samples) != args.repeats:
                raise ValueError("Native timing sample count changed")
            native = {key: subphase_distribution([sample[key] for sample in native_samples]) for key in native_samples[0]}
            results.append(dict(case_id=row["case_id"], beam=row["beam"], lengths=row["lengths"], sequence=row["compared_sequence"],
                structure=row["reference_structure"], energy=row["reference_energy"], all_predictions_match_reference=True,
                uninstrumented_before=before, uninstrumented_after=after, phases=phases, call_profile=profile,
                chart=diagnostics, native=native, native_samples=native_samples))
            shares = phases["aggregate_time_fraction"]
            print(f"{row['case_id']} beam={row['beam']}: warm API {before['median_s']*1000:.3f} ms; "
                  f"traceback {shares['traceback']:.1%}, inside {shares['inside']:.1%}, energy {shares['energy']:.1%}; "
                  f"max P/column {diagnostics['maximum_pairs_per_column']}", flush=True)
    if signatures_before != compiled_signatures():
        raise ValueError("A new JIT signature appeared during warmed profiling")
    if comparator.manifest(reference) != baseline["reference_source_sha256"]:
        raise ValueError("Reference source changed during profiling")
    data = dict(run_utc=datetime.now(timezone.utc).isoformat(), baseline=str(args.baseline.resolve()),
        baseline_sha256=comparator.sha(args.baseline), reference=str(reference),
        fixtures=baseline["fixtures"], parameters=benchmark.PARAMETERS, repeats=args.repeats,
        warmup_calls_per_setting=3, traceback_mode="numba_single_best" if fast_traceback else "python_kbest",
        python=sys.version, platform=platform.platform(), numba=numba.__version__,
        numpy=np.__version__, nopython_signatures=signatures_before,
        compiler_flags=comparator.FLAGS, compiler_stderr=build.stderr,
        reference_source_sha256=baseline["reference_source_sha256"], cplfold_source_sha256=baseline["cplfold_source_sha256"],
        profiling_source_sha256={str(path.relative_to(ROOT)): comparator.sha(path) for path in
            [Path(__file__).resolve(), harness, ROOT / "scripts/benchmark-cplcofold-local.py", ROOT / "scripts/compare-cplcofold-linearcofold.py"]},
        results=results)
    comparator.write_exports(args.output, {"results.json": comparator.json_text(data), "report.md": render_report(data),
        "fixtures.json": comparator.json_text(baseline["fixtures"]), "linearcofold-profile.cpp": harness.read_text()})
    print(f"Wrote {args.output / 'report.md'}", flush=True)


def render_report(data):
    rows = data["results"]
    fast_traceback = data["traceback_mode"] == "numba_single_best"
    report = ["# Warm CPLcofold performance breakdown", "", f"Run: {data['run_utc']}", "",
        "All imports, JIT compilation, cache loading, and warmups are excluded. Alpha=0, beta=0, no pseudoknots, "
        "max_phase1=1, energy_delta=0. Every measured prediction matches the previously validated original reference structure and energy. "
        "Parser/evaluator nopython signatures are unchanged before/after measurement.", "",
        "## Phase timings", "",
        "Medians in milliseconds. Preparation excludes the separately timed face/inside kernels. Percentages below are ratios of summed phase times "
        "to summed instrumented API times, so exclusive shares sum to 100%. Medians of phases need not sum to the median total. "
        f"Each setting has {data['repeats']} samples; raw samples are retained in results.json.", "",
        "| Case | Beam | Uninstrumented API before/after | Instrumented API total | Numba inside | Numba faces | Preparation | "
        + ("Numba single-best traceback" if fast_traceback else "Python-led k-best traceback") + " | Energy rescore | Formatting | Other API |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|"]
    for row in rows:
        phases = row["phases"]["statistics"]
        values = " | ".join(f"{phases[key]['median_s']*1000:.3f}" for key in
            ("inside", "faces", "preparation", "traceback", "energy", "formatting", "api_other"))
        report.append(f"| {row['case_id']} | {row['beam']} | {row['uninstrumented_before']['median_s']*1000:.3f} / "
            f"{row['uninstrumented_after']['median_s']*1000:.3f} | {phases['total']['median_s']*1000:.3f} | {values} |")
    report += ["", "The before/after columns are uninstrumented full API calls. Instrumentation and run-to-run host variation can change observed latency; "
        "phase percentages characterize the instrumented calls and are not exact uninstrumented shares. "
        "cProfile is run separately "
        "and is used to inspect function calls, not to estimate uninstrumented latency or the phase percentages.", "",
        "| Case | Beam | Traceback share | Inside share | Rescoring share | Legacy-oracle records | Legacy-oracle candidate edges | Max paired states/column | Charts identical to beam=0 |",
        "|---|---:|---:|---:|---:|---:|---:|---:|---|"]
    for row in rows:
        share, chart = row["phases"]["aggregate_time_fraction"], row["chart"]
        report.append(f"| {row['case_id']} | {row['beam']} | {share['traceback']:.1%} | {share['inside']:.1%} | {share['energy']:.1%} | "
            f"{chart['legacy_oracle_records']} | {chart['legacy_oracle_retained_edges']} | {chart['maximum_pairs_per_column']} | "
            f"{chart['pair_chart_equals_beam0'] and chart['m1_chart_equals_beam0'] and chart['m2_chart_equals_beam0']} |")
    report += ["", "Legacy-oracle counts come from a separate diagnostic call, outside all timings. They describe the general k-best graph, "
        "not work performed by the optimized single-best path.", "", "## Quiet original C++ parser", "",
        "The same source and optimization flags are used as in the baseline. No verbose printing is enabled. "
        "Stack construction, parse/traceback, and destruction are measured separately; validation and TSV output occur outside timers. "
        "Native constructor samples below clock resolution are retained as zero. "
        "The original parser's parse phase includes allocation/preparation, dynamic programming, and traceback, so it cannot be equated to the Python inside kernel alone.", "",
        "| Case | Beam | Constructor ms | Parse + traceback ms | Destructor ms | Total ms |",
        "|---|---:|---:|---:|---:|---:|"]
    for row in rows:
        values = " | ".join(f"{row['native'][key]['median_s']*1000:.3f}" for key in ("constructor", "parse", "destruction", "total"))
        report.append(f"| {row['case_id']} | {row['beam']} | {values} |")
    report += ["", "## Interpretation", "",
        ("Single-result calls now use a compiled traversal of the retained forest, preserving edge order and float arithmetic while avoiding Python heaps. "
         "The general Python k-best enumerator remains available for multiple candidates. " if fast_traceback else
         "Single-result calls use the Python k-best enumerator, initializing candidate heaps even when only rank zero is requested. ")
        + "The native parser records predecessor choices during DP and follows those choices in traceback. "
        "These are different implementations with different amounts of work, not a controlled compiler comparison.", "",
        "Numba JIT is not automatic whole-program compilation or parallelization. Current kernels use njit(cache=True), without parallel=True. "
        "The dense Python chart also has different pruning/scaling from LinearCoFold. The charts for these particular short windows should be read from the diagnostics above; "
        "the results do not predict long-sequence scaling. The shared host is uncontrolled and warm timings can vary between runs.", "",
        "Use the current measured phase shares to prioritize further work; exact tie ordering, constraints, alpha evidence, and phase-2 behavior must be retained.", "",
        "Reproduce with `.venv-cplfold/bin/python scripts/profile-cplcofold-local.py --baseline " + data["baseline"]
        + " --output artifacts/cplcofold-local-optimized-profile`. Full per-call distributions, function-call profiles, "
        "chart counts, pinned fixtures and source hashes are in results.json.", ""]
    return "\n".join(report)


if __name__ == "__main__":
    main()
