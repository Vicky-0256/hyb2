#!/usr/bin/env python3
"""Compare biological RNA fixtures with a read-only local LinearCoFold tree."""
from __future__ import annotations

import argparse
import csv
from datetime import datetime, timezone
import hashlib
import io
import json
import math
import os
from pathlib import Path
import platform
import re
import shlex
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[1]
OUTPUT = ROOT / "artifacts" / "cplcofold-comparison"
FLAGS = ["-std=c++11", "-O3", "-Dlv", "-Dis_cube_pruning", "-Dis_candidate_list"]


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def manifest(reference):
    return {str(p.relative_to(reference)): sha(p) for p in sorted((reference / "src").rglob("*"))
            if p.is_file() and p.suffix in {".h", ".cpp"}}


def pairs(structure):
    stack, result = [], set()
    for i, ch in enumerate(structure.replace("&", "")):
        if ch == "(":
            stack.append(i)
        elif ch == ")":
            if not stack:
                raise ValueError("Unbalanced structure")
            result.add((stack.pop(), i))
        elif ch != ".":
            raise ValueError("Unexpected structure character")
    if stack:
        raise ValueError("Unbalanced structure")
    return result


def validate_structure(sequence, structure):
    """Validate the displayed two-strand layout before using compact indices."""
    if not isinstance(sequence, str) or not re.fullmatch(r"[ACGU]+&[ACGU]+", sequence):
        raise ValueError("Expected two nonempty ACGU strands separated by '&'")
    if (not isinstance(structure, str) or len(structure) != len(sequence)
            or structure.count("&") != 1 or structure.index("&") != sequence.index("&")):
        raise ValueError("Structure length or strand boundary does not match the sequence")
    return sorted(pairs(structure))


def finite_energy(value):
    if isinstance(value, (bool, str)) or not isinstance(value, (int, float)) or not math.isfinite(value):
        raise ValueError("All comparison energies must be finite numbers")
    return float(value)


def compare_prediction(sequence, prediction, reference_structure, reference_energy,
                       reference_rescored_energy, prediction_rescored_energy):
    predicted_pairs = validate_structure(sequence, prediction["structure"])
    reference_pairs = validate_structure(sequence, reference_structure)
    energy, reference_energy, reference_rescored_energy, prediction_rescored_energy = map(
        finite_energy, (prediction["energy"], reference_energy,
                        reference_rescored_energy, prediction_rescored_energy))
    energy_equal = abs(energy - reference_energy) < 1e-7
    ref_scoring_equal = abs(reference_rescored_energy - reference_energy) < 1e-7
    pred_scoring_equal = abs(prediction_rescored_energy - energy) < 1e-7
    same_pairs = predicted_pairs == reference_pairs
    same_structure = prediction["structure"] == reference_structure
    if not (ref_scoring_equal and pred_scoring_equal):
        status = "scoring_mismatch"
    elif not energy_equal:
        status = "energy_difference"
    else:
        status = "identical" if same_structure and same_pairs else "equal_energy_tie"
    return dict(structure=prediction["structure"], reference_structure=reference_structure,
                energy=energy, reference_energy=reference_energy,
                reference_rescored_energy=reference_rescored_energy,
                prediction_rescored_energy=prediction_rescored_energy,
                reference_scoring_equal=ref_scoring_equal, prediction_scoring_equal=pred_scoring_equal,
                energy_equal=energy_equal, pair_sets_equal=same_pairs, structure_equal=same_structure,
                pair_symmetric_difference=len(set(predicted_pairs) ^ set(reference_pairs)),
                pairs=predicted_pairs, reference_pairs=reference_pairs, status=status,
                comparison_ok=status in {"identical", "equal_energy_tie"})


def validate_fixtures(fixtures):
    cases = fixtures.get("cases")
    if not isinstance(cases, list) or not cases:
        raise ValueError("Fixtures must contain at least one case")
    seen = set()
    for case in cases:
        identifier = case.get("id", "")
        if not re.fullmatch(r"[A-Za-z0-9_-]+", identifier) or identifier in seen:
            raise ValueError("Case IDs must be unique, nonempty file-safe identifiers")
        seen.add(identifier)
        for key in ("a", "b"):
            seq = case.get("sequence_" + key)
            if not isinstance(seq, str) or not re.fullmatch("[ACGU]+", seq):
                raise ValueError("Fixture sequences must be nonempty ACGU strings")
            if "source_regions" in case:
                region = case["source_regions"].get(key, {})
                start, end = region.get("start"), region.get("end")
                if (type(start) is not int or type(end) is not int or start < 1
                        or end - start + 1 != len(seq)):
                    raise ValueError("Source region must match the window's 1-based inclusive coordinates")
                total = region.get("source_sequence_length", end)
                if type(total) is not int or total < end:
                    raise ValueError("Source window exceeds the source sequence")


def compared_order(case, sequence):
    listed = case["sequence_a"] + "&" + case["sequence_b"]
    if sequence == listed:
        return ("a", "b")  # Homodimers preserve the original labelled identities.
    if sequence == case["sequence_b"] + "&" + case["sequence_a"]:
        return ("b", "a")
    raise ValueError("Compared sequence is neither the listed nor reversed strand order")


PAIR_COLUMNS = ["rna_1", "window_position_1", "source_position_1", "rna_2",
                "window_position_2", "source_position_2", "pair_type"]


def mapped_pairs(case, row):
    validate_fixtures({"cases": [case]})
    sequence = row["compared_sequence"]
    order = compared_order(case, sequence)
    cutpoint = sequence.index("&")
    regions = case.get("source_regions", {})
    result = []
    for i, j in validate_structure(sequence, row["structure"]):
        record = {}
        for endpoint, index in enumerate((i, j), 1):
            key = order[int(index >= cutpoint)]
            local = index - (cutpoint if index >= cutpoint else 0) + 1
            region = regions.get(key)
            record["rna_" + str(endpoint)] = region["rna"] if region else case.get("strand_" + key, "strand_" + key)
            record["window_position_" + str(endpoint)] = local
            record["source_position_" + str(endpoint)] = region["start"] + local - 1 if region else None
        record["pair_type"] = "intermolecular" if i < cutpoint <= j else "intramolecular"
        result.append(record)
    return result


def json_text(value):
    return json.dumps(value, indent=2, sort_keys=True, allow_nan=False) + "\n"


def build_exports(data):
    """Build all auxiliary outputs from this run, including each tested beam."""
    fixtures = data["fixtures"]
    validate_fixtures(fixtures)
    files = {}
    case_ids = {case["id"] for case in fixtures["cases"]}
    if any(row["case_id"] not in case_ids for row in data["results"]):
        raise ValueError("Results refer to a case absent from the fixtures")
    for case in fixtures["cases"]:
        prefix = "" if len(case_ids) == 1 else "cases/" + case["id"] + "/"
        rows = [r for r in data["results"] if r["case_id"] == case["id"]]
        if not rows or len({r["beam"] for r in rows}) != len(rows):
            raise ValueError("Each case must have nonempty results with unique beams")
        fasta = []
        for key in ("a", "b"):
            label = str(case.get("strand_" + key, "strand_" + key)).replace("\n", " ").replace("\r", " ")
            fasta.extend([f">{case['id']}_{key} {label}", case["sequence_" + key]])
        files[prefix + "input.fasta"] = "\n".join(fasta) + "\n"
        beam_summaries = []
        for row in rows:
            beam = row["beam"]
            if type(beam) is not int or beam < 0:
                raise ValueError("Beam size must be a nonnegative integer")
            mapped = mapped_pairs(case, row)
            buffer = io.StringIO(newline="")
            writer = csv.DictWriter(buffer, fieldnames=PAIR_COLUMNS, delimiter="\t", lineterminator="\n")
            writer.writeheader()
            writer.writerows(mapped)
            files[prefix + f"pairs-beam{beam}.tsv"] = buffer.getvalue()
            beam_summaries.append(dict(beam=beam, sequence=row["compared_sequence"], structure=row["structure"],
                energy_kcal_mol=finite_energy(row["energy"]), cutpoint=row["compared_sequence"].index("&"),
                strand_lengths=[len(s) for s in row["compared_sequence"].split("&")],
                strand_order=list(compared_order(case, row["compared_sequence"])), base_pairs=len(mapped),
                intermolecular_pairs=sum(p["pair_type"] == "intermolecular" for p in mapped),
                status=row["status"], comparison_ok=row["comparison_ok"],
                reference_scoring_equal=row["reference_scoring_equal"],
                prediction_scoring_equal=row["prediction_scoring_equal"]))
        primary = next((r for r in beam_summaries if r["beam"] == 100), beam_summaries[0])
        summary = dict(primary, case_id=case["id"], run_utc=data["run_utc"],
            fixture_sha256=data["fixture_sha256"], source_regions=case.get("source_regions"),
            window_coordinates="1-based inclusive deposited FASTA positions" if "source_regions" in case else None,
            listed_sequence=case["sequence_a"] + "&" + case["sequence_b"],
            input_fasta_order=["a", "b"], beam_sizes=[r["beam"] for r in rows],
            beams=beam_summaries, numba_enabled=not data["numba_disabled"], **data["parameters"])
        summary["reference_matches_both_beams"] = ({r["beam"] for r in rows} == {100, 0}
            and all(r["status"] == "identical" and r["comparison_ok"] for r in rows))
        files[prefix + "summary.json"] = json_text(summary)
    return files


def _safe_export_path(output, name):
    path = Path(name)
    if (not name or path.is_absolute() or ".." in path.parts
            or name == "exports.json" or str(path) != name):
        raise ValueError("Unsafe managed export path")
    current = output
    for part in path.parts:
        current = current / part
        if current.is_symlink():
            raise ValueError("Managed export paths must not contain symbolic links")
    target = output / path
    if not target.resolve().is_relative_to(output.resolve()):
        raise ValueError("Export path escapes the output directory")
    return target


def write_exports(output, files):
    """Stage files, replace each atomically, then publish a hash manifest last.

    Only obsolete paths owned by the prior manifest (or the four legacy root
    auxiliary files) are removed. This is not a directory-wide transaction;
    a failed write can be detected by checking the last published manifest.
    """
    output = Path(output)
    targets = {name: _safe_export_path(output, name) for name in files}
    old = set()
    manifest_path = output / "exports.json"
    if manifest_path.exists():
        old.update(json.loads(manifest_path.read_text())["files"])
    old.update({"input.fasta", "summary.json", "pairs-beam100.tsv", "pairs-beam0.tsv"})
    obsolete = [_safe_export_path(output, name) for name in old - files.keys()]
    encoded = {name: content.encode("utf-8") for name, content in files.items()}
    export_manifest = json_text(dict(schema_version=1, files={name: hashlib.sha256(content).hexdigest()
                                                           for name, content in encoded.items()}))
    output.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".exports-", dir=output) as directory:
        stage = Path(directory)
        for name, content in encoded.items():
            path = stage / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_bytes(content)
        (stage / "exports.json").write_text(export_manifest)
        for name, target in targets.items():
            target.parent.mkdir(parents=True, exist_ok=True)
            os.replace(stage / name, target)
        for path in obsolete:
            if path.is_file():
                path.unlink()
        os.replace(stage / "exports.json", manifest_path)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", type=Path, default=ROOT.parent / "VincentZhangHe-cofold-copartition-13c0beee5b11")
    parser.add_argument("--fixtures", type=Path, default=OUTPUT / "fixtures.json")
    parser.add_argument("--output", type=Path, default=OUTPUT)
    parser.add_argument("--compiler", default="g++")
    args = parser.parse_args()
    reference = args.reference.resolve()
    fixture_bytes = args.fixtures.read_bytes()
    fixtures = json.loads(fixture_bytes)
    validate_fixtures(fixtures)
    before = manifest(reference)
    sys.path.insert(0, str(ROOT / "third_party" / "cplfold"))
    from CPLfold import cofold
    from Utils.cofold_energy import CofoldEnergy
    import numba
    import numpy
    evaluator = CofoldEnergy()
    rows = []
    with tempfile.TemporaryDirectory(prefix="cplcofold-reference-", dir="/tmp") as directory:
        binary = Path(directory) / "linearcofold_v"
        command = [args.compiler, str(reference / "src" / "LinearCoFold.cpp"), *FLAGS, "-o", str(binary)]
        compile_result = subprocess.run(command, capture_output=True, text=True, check=True, timeout=300)
        binary_sha = sha(binary)
        for case in fixtures["cases"]:
            listed = case["sequence_a"] + "&" + case["sequence_b"]
            for beam in (100, 0):
                run = subprocess.run([str(binary), str(beam), "0", "0"], input=listed + "\n",
                                     text=True, capture_output=True, check=True, timeout=300)
                lines = run.stdout.strip().splitlines()
                actual = next(line for line in lines if re.fullmatch(r"[ACGU]+&[ACGU]+", line))
                a, b = actual.split("&")
                if actual not in (listed, case["sequence_b"] + "&" + case["sequence_a"]):
                    raise ValueError("Reference changed an input sequence")
                match = next(re.fullmatch(r"([.()]+&[.()]+)\s+\((-?\d+\.\d+)\)", line)
                             for line in lines if re.fullmatch(r"[.()]+&[.()]+\s+\(-?\d+\.\d+\)", line))
                ref_structure, ref_energy = match.group(1), float(match.group(2))
                result = cofold(a, b, beam_size=beam, alpha=0.0, beta=0.0, allow_pseudoknot=False,
                                energy_delta=0.0, max_phase1=1, verbose=False)[0]
                validate_structure(actual, ref_structure)
                validate_structure(actual, result["structure"])
                rescore = evaluator.compute_energy(actual, ref_structure, len(a))["energy"]
                prediction_rescore = evaluator.compute_energy(actual, result["structure"], len(a))["energy"]
                comparison = compare_prediction(actual, result, ref_structure, ref_energy, rescore, prediction_rescore)
                row = dict(case_id=case["id"], biological_provenance_verified=case["biological_provenance_verified"],
                           beam=beam, listed_sequence=listed, compared_sequence=actual, lengths=[len(a), len(b)],
                           reference_swapped_strands=actual != listed, **comparison,
                           reference_stdout=run.stdout, sources=case["sources"])
                rows.append(row)
                print(f"{case['id']}: beam={beam}, CPL={result['energy']:.2f}, reference={ref_energy:.2f}, {row['status']}", flush=True)
    if manifest(reference) != before:
        raise RuntimeError("Reference source changed during comparison")
    data = dict(run_utc=datetime.now(timezone.utc).isoformat(), reference=str(reference), compiler_flags=FLAGS,
                comparison_script_sha256=sha(Path(__file__)),
                compiler_version=subprocess.run([args.compiler,"--version"],capture_output=True,text=True,check=True).stdout.splitlines()[0],
                compiler_stderr=compile_result.stderr, reference_source_sha256=before, reference_binary_sha256=binary_sha,
                python=sys.version, platform=platform.platform(), numpy=numpy.__version__, numba=numba.__version__,
                numba_disabled=bool(numba.config.DISABLE_JIT), fixture_sha256=hashlib.sha256(fixture_bytes).hexdigest(), fixtures=fixtures,
                parameters=dict(alpha=0.0,beta=0.0,allow_pseudoknot=False,energy_delta=0.0,max_phase1=1),
                cplfold_source_sha256={name:sha(ROOT/"third_party"/"cplfold"/name) for name in
                                      ["CPLfold.py","CPLcofold.py","CPLcofold_parser.py","Utils/cofold_energy.py"]},
                results=rows)
    biological = [r for r in rows if r["biological_provenance_verified"]]
    report = ["# CPLcofold / LinearCoFold comparison", "", f"Run: {data['run_utc']}", "",
              f"{len({r['case_id'] for r in biological})} verified biological RNA pairs: {sum(r['energy_equal'] for r in biological)}/{len(biological)} energy matches, "
              f"{sum(r['pair_sets_equal'] for r in biological)}/{len(biological)} pair-set matches, "
              f"{sum(r['comparison_ok'] for r in biological)}/{len(biological)} validated comparisons."
              + (" Cases without verified biological provenance are counted separately."
                 if len(biological) != len(rows) else ""), "",
              "This checks agreement between software given the same sequences and model. It does not assess prediction accuracy against native experimental structures or measured binding energies. "
              "RCSB supplies sequence provenance, not secondary-structure ground truth here; proteins, modifications and noncanonical contacts are omitted.", "",
              "Public API settings: alpha=0, beta=0, allow_pseudoknot=False, energy_delta=0, max_phase1=1. "
              "Upstream sorts strands by length; CPLcofold receives the exact printed reference order. Both listed and compared sequences are recorded. "
              "Beam 0 disables pruning but retains the 30-nt ordinary internal-loop cap. The two algorithms prune different state families, so finite-beam agreement is empirical. No timing claim is made.", "",
              "| Case | Lengths in compared order | Beam | CPL kcal/mol | Reference kcal/mol | Pair differences | Outcome |",
              "|---|---|---:|---:|---:|---:|---|"]
    for r in rows:
        report.append(f"| {r['case_id']} | {r['lengths'][0]}+{r['lengths'][1]} | {r['beam']} | {r['energy']:.2f} | {r['reference_energy']:.2f} | {r['pair_symmetric_difference']} | {r['status']} |")
    report += ["", "Both predicted and reference structures must have the correct length, strand boundary and balanced brackets. "
               "Both energies must be finite and match independent rescoring. Only then are equal-energy structures accepted; "
               "different structures are labelled `equal_energy_tie`. Scoring or energy mismatches cause a nonzero process exit. "
               "Full details are in results.json.", "", "## Sequence provenance", ""]
    for case in fixtures["cases"]:
        report += [f"### {case['name']}", "", case["strand_a"]+" + "+case["strand_b"], "", case["notes"], ""]
        report += [f"- [Source {i+1}]({url})" for i,url in enumerate(case["sources"]) if url.startswith("https://")]
        report.append("")
    reproduce = (["env", "NUMBA_DISABLE_JIT=1"] if data["numba_disabled"] else []) + [
        ".venv-cplfold/bin/python", "scripts/compare-cplcofold-linearcofold.py",
        "--reference", str(reference), "--fixtures", str((args.output / "fixtures.json").resolve()),
        "--output", str(args.output.resolve()), "--compiler", args.compiler,
    ]
    report += ["## Reproduce", "", "```sh", shlex.join(reproduce), "```", "",
               "The reference compiles only into /tmp with `"+" ".join(FLAGS)+"`. Source hashes are checked unchanged. "
               "Pinned sequences and provenance are in fixtures.json; exact compiler/version/source hashes and outputs are in results.json. "
               "Each run also refreshes input.fasta, pairs-beam*.tsv and summary.json (under cases/<id>/ for multiple cases). "
               "FASTA retains the listed strand order; summaries and pair tables identify the compared strand order and remap local source coordinates. "
               "Coordinates exclude '&'. The final exports.json manifest records hashes for all managed files.", ""]
    files = build_exports(data)
    files.update({"fixtures.json": fixture_bytes.decode("utf-8"), "results.json": json_text(data),
                  "report.md": "\n".join(report)})
    write_exports(args.output, files)
    print(f"Wrote synchronized comparison outputs to {args.output}")
    return 0 if all(r["comparison_ok"] for r in rows) else 1


if __name__ == "__main__":
    sys.exit(main())
