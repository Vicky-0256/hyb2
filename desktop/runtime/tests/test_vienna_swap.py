"""Evaluation cases for replacing UNAFold hybrid-min with ViennaRNA RNAduplex.

hybrid-min does two jobs in HYB2, and both move to ViennaRNA's RNAduplex
(``RNA.duplexfold``), which, like hybrid-min, forms intermolecular pairs only:

* the .hyb dG column (``hyb2pipe`` energy engine ``hybrid-min``);
* the C_ij counts of the saturating support kernel (``hybridmin`` in
  web/cplfold/cplfold_web.py), B_ij = s C_ij / (C_ij + q).

``Invariants`` hold before and after the replacement: the block kernels, the
saturating transform, the fold it drives, and the path that receives counts
from the host. Gaussian's subsequently adopted IRIS precision and cutoff
are checked against an independent reference in test_support_kernels.py.

``Targets`` check the RNAduplex support kernel, default energy engines and
the energy column in .hyb output.

``ZikvRegression`` compares the new C_ij with the cached hybrid-min C_ij of
the ZIKV genome folds (kernel_experiments/jobs/zikv_new_b, skipped when
absent).  The window part runs by default; ``HYB2_SLOW=1`` adds the
genome-wide comparison (about 25 minutes).

Run everything with ``scripts/check-vienna-swap.sh``.
"""
import hashlib
import json
import math
import os
import re
import statistics
import subprocess
import sys
import tempfile
import unittest
from collections import Counter
from contextlib import contextmanager
from importlib.util import find_spec
from pathlib import Path
from unittest import mock

from desktop.runtime import structure

ROOT = Path(__file__).resolve().parents[3]
WORKSPACE = ROOT.parent
HERE = Path(__file__).resolve().parent
REFERENCE = HERE / "vienna_swap"
if str(ROOT / "pipeline") not in sys.path:
    sys.path.insert(0, str(ROOT / "pipeline"))

NEW_KERNEL = "duplex"             # replaces the support kernel "hybridmin"
NEW_ENERGY_ENGINE = "rnaduplex"   # replaces the .hyb dG engine "hybrid-min"

SLOW = os.environ.get("HYB2_SLOW") == "1"
VIENNA_VERSION = "2.7.2"          # every reference value below was computed with it
SATURATING_SCALE = 3.6


# --------------------------------------------------------------------------
# fixtures
# --------------------------------------------------------------------------

SEQUENCE = "GGGAAACCCAGCUUCGGCUGGGAAACCCAAAGGCUAGCUAGCCUUUUAGCGCGAAAGCGCU"
ARMS = [(1, 9, 25, 33), (1, 9, 25, 33), (10, 20, 15, 24), (34, 44, 50, 60)]
# kernels the replacement does not touch: (sha256 prefix of the bonus entries, entries)
DIGESTS = {
    "block": ("1b676f7cb53f64d2", 291),
    "block-normalized": ("0a39abf61a835f0b", 291),
}
# counts the host passes (evidencePairs): q = mean of the round(62 / 3) = 21 best = 2.8
HOST_PAIRS = [[3, 30, 2], [5, 28, 4], [6, 27, 4], [7, 26, 3], [11, 57, 1]]
# counts that change the fold at alpha 1 but not at alpha 0.3
FLIP_PAIRS = [[29, 47, 50], [30, 46, 50], [31, 45, 50], [32, 44, 50]]
MFE = "(((...((((((....))))))...)))(((((((....))))))).(((((....)))))"
FLIPPED = "(((...((((((....))))))...)))(((((((....)))).)))(((((....)))))"

# The new kernel on arms: a sequence whose first arm pair is purine-only (no
# hybrid: RNAduplex returns ".&." with dG 100000), then three ordinary pairs.
ARM_SEQUENCE = "AAAAAAAAAAGAGAGAAAAA" + SEQUENCE
ARM_ROWS = [(1, 8, 11, 18)] + [(a + 20, b + 20, c + 20, d + 20) for a, b, c, d in ARMS]


def _payload(sequence=SEQUENCE, arms=ARMS, **extra):
    payload = {"sequence": sequence, "evidenceMode": "hyb-blocks", "maxSequenceLength": 120, "beamSize": 20,
               "allowPseudoknot": False,
               "evidenceArms": [{"oneStart": a, "oneEnd": b, "twoStart": c, "twoEnd": d} for a, b, c, d in arms]}
    payload.update(extra)
    return payload


def _digest(entries):
    rows = sorted((r["one"], r["two"], round(float(r["value"]), 7)) for r in entries)
    return hashlib.sha256(json.dumps(rows).encode()).hexdigest()[:16], len(rows)


def _entries(evidence):
    return {(r["one"], r["two"]): float(r["value"]) for r in evidence["bonusEntries"]}


def _expected_bonus(counts, sequence_length, q=None):
    if q is None:
        top = sorted(counts.values(), reverse=True)[:max(1, round(sequence_length / 3))]
        q = sum(top) / len(top)
    return {pair: SATURATING_SCALE * c / (c + q) for pair, c in counts.items()}, q


def _rna():
    if find_spec("RNA") is None:
        raise unittest.SkipTest("ViennaRNA's Python module is not installed")
    import RNA  # noqa: PLC0415
    return RNA


def reference_duplex_pairs(sequence, rows):
    """C_ij computed here, independently of HYB2: RNAduplex on each row's two arms,
    one count per row for every pair it forms (1-based, i < j)."""
    RNA = _rna()
    counts = Counter()
    for (a, b, c, d), weight in Counter(rows).items():
        one, two = sequence[a - 1:b], sequence[c - 1:d]
        hit = RNA.duplexfold(one, two)
        left, right = hit.structure.split("&")
        start_one, start_two = hit.i - len(left) + 1, hit.j
        opens = [k for k, ch in enumerate(left) if ch == "("]
        closes = [k for k, ch in enumerate(right) if ch == ")"]
        for x, y in zip(opens, reversed(closes)):
            i, j = a + start_one - 1 + x, c + start_two - 1 + y
            if i != j:
                counts[(min(i, j), max(i, j))] += weight
    return counts


def reference_duplex_dg(one, two):
    """RNAduplex dG of two arms, None when they form no pair (RNAduplex: ".&.", 100000)."""
    hit = _rna().duplexfold(one, two)
    return None if "(" not in hit.structure else hit.energy


@contextmanager
def no_hybrid_min(tmp):
    """hybrid-min cannot be found or run: a configured path that does not exist,
    and a PATH with only this interpreter's directory."""
    empty = Path(tmp) / "empty-bin"
    empty.mkdir(exist_ok=True)
    path = os.pathsep.join([str(empty), str(Path(sys.executable).parent)])
    with mock.patch.dict(os.environ, {"HYB2_HYBRID_MIN": str(Path(tmp) / "no-such-hybrid-min"), "PATH": path}):
        yield


@contextmanager
def no_vienna():
    with mock.patch.dict(sys.modules, {"RNA": None}):
        yield


class _Web(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if find_spec("numpy") is None:
            raise unittest.SkipTest("NumPy is not installed")
        cls.web = structure.load_cplfold_web(ROOT)

    def saturating_kernel(self):
        return NEW_KERNEL if NEW_KERNEL in self.web.SUPPORT_KERNELS else "hybridmin"


# --------------------------------------------------------------------------
# invariants: pass before and after
# --------------------------------------------------------------------------

class Invariants(_Web):
    def test_vienna_version(self):
        self.assertEqual(_rna().__version__, VIENNA_VERSION)

    def test_untouched_kernels_are_unchanged(self):
        for kernel, expected in DIGESTS.items():
            with self.subTest(kernel=kernel):
                evidence = self.web.bonus_matrix(_payload(supportKernel=kernel))["evidence"]
                self.assertEqual(_digest(evidence["bonusEntries"]), expected)

    def test_saturating_transform_of_host_counts(self):
        counts = Counter({(i, j): c for i, j, c in HOST_PAIRS})
        for q in (None, 10.0):
            with self.subTest(q=q):
                extra = {} if q is None else {"saturatingQ": q}
                evidence = self.web.bonus_matrix(_payload(supportKernel=self.saturating_kernel(),
                                                          evidencePairs=HOST_PAIRS, **extra))["evidence"]
                expected, used_q = _expected_bonus(counts, len(SEQUENCE), q)
                self.assertAlmostEqual(evidence["saturatingQ"], used_q, places=9)
                got = _entries(evidence)
                self.assertEqual(set(got), set(expected))
                for pair, value in expected.items():
                    self.assertAlmostEqual(got[pair], value, places=5, msg=str(pair))
                self.assertAlmostEqual(evidence["maximumBonus"], max(expected.values()), places=5)

    def test_host_counts_change_the_fold_only_at_high_alpha(self):
        for alpha, expected, energy in ((0.3, MFE, -32.0), (1.0, FLIPPED, -28.4)):
            with self.subTest(alpha=alpha):
                result = self.web.fold(_payload(arms=[(1, 9, 25, 33)], supportKernel=self.saturating_kernel(),
                                                alpha=alpha, evidencePairs=FLIP_PAIRS))
                self.assertEqual(result["dotBracket"], expected)
                self.assertAlmostEqual(result["energy"], energy, places=2)

    def test_host_counts_need_neither_engine(self):
        with tempfile.TemporaryDirectory() as tmp, no_hybrid_min(tmp), no_vienna():
            result = self.web.fold(_payload(supportKernel=self.saturating_kernel(), alpha=0.5,
                                            evidencePairs=HOST_PAIRS))
        self.assertEqual(result["evidence"]["supportedPairs"], len(HOST_PAIRS))


# --------------------------------------------------------------------------
# targets: RNAduplex support kernel, energy defaults and .hyb output
# --------------------------------------------------------------------------

# hybrid-min by name.  UNAFold's structure folder hybrid-ss-min (the legacy
# "-r 0" folding mode, from the same OligoArrayAux package) is a different
# program and is not part of this replacement.
SCAN = re.compile(r"hybrid[-_ ]?min|HYBRID_MIN", re.IGNORECASE)
# The only lines that may still name hybrid-min: the ones that read what was saved
# before the replacement (an old project's hybrid-min path, a result's old kernel
# name), each marked with this comment.
LEGACY_MARK = "legacy-name:"
SCAN_SKIP_PREFIXES = ("VARNA/", "desktop/runtime/tests/vienna_swap/", "third_party/cplfold/LICENSES/")
SCAN_SKIP_FILES = {"desktop/runtime/tests/test_vienna_swap.py", "scripts/check-vienna-swap.sh"}
SCAN_SKIP_SUFFIXES = (".md", ".txt", ".pdf", ".png", ".svg", ".class", ".jar")


def hybrid_min_mentions():
    files = subprocess.run(["git", "ls-files", "-z"], cwd=ROOT, capture_output=True, check=True).stdout
    found = []
    for name in files.decode().split("\0"):
        if (not name or name in SCAN_SKIP_FILES or name.startswith(SCAN_SKIP_PREFIXES)
                or name.endswith(SCAN_SKIP_SUFFIXES)):
            continue
        path = ROOT / name
        try:
            data = path.read_bytes()
        except OSError:
            continue
        if b"\0" in data:
            continue
        for number, line in enumerate(data.decode("utf-8", "replace").splitlines(), 1):
            if SCAN.search(line) and LEGACY_MARK not in line:
                found.append(f"{name}:{number}: {line.strip()[:100]}")
    return found


class Targets(_Web):
    def test_no_hybrid_min_left_in_the_code(self):
        found = hybrid_min_mentions()
        files = sorted({line.split(":", 1)[0] for line in found})
        self.assertEqual(found, [], "%d lines in %d files still use hybrid-min:\n%s"
                         % (len(found), len(files), "\n".join(found[:60])))

    def test_the_new_kernel_replaces_hybridmin(self):
        self.assertIn(NEW_KERNEL, self.web.SUPPORT_KERNELS)
        self.assertNotIn("hybridmin", self.web.SUPPORT_KERNELS)
        self.assertEqual(tuple(self.web.SUPPORT_KERNELS), tuple(structure.SUPPORT_KERNELS))

    def test_the_new_kernel_counts_rnaduplex_pairs(self):
        counts = reference_duplex_pairs(ARM_SEQUENCE, ARM_ROWS)
        self.assertTrue(counts)
        self.assertFalse(any(i <= 18 for i, _ in counts), "the purine-only arms must add nothing")
        evidence = self.web.bonus_matrix(_payload(sequence=ARM_SEQUENCE, arms=ARM_ROWS,
                                                  supportKernel=NEW_KERNEL))["evidence"]
        expected, q = _expected_bonus(counts, len(ARM_SEQUENCE))
        self.assertAlmostEqual(evidence["saturatingQ"], q, places=9)
        got = _entries(evidence)
        self.assertEqual(set(got), set(expected))
        for pair, value in expected.items():
            self.assertAlmostEqual(got[pair], value, places=5, msg=str(pair))

    def test_the_new_kernel_runs_without_hybrid_min(self):
        with tempfile.TemporaryDirectory() as tmp, no_hybrid_min(tmp):
            result = self.web.fold(_payload(sequence=ARM_SEQUENCE, arms=ARM_ROWS, supportKernel=NEW_KERNEL,
                                            alpha=0.5))
        self.assertEqual(result["evidence"]["supportKernel"], NEW_KERNEL)
        self.assertGreater(result["evidence"]["supportedPairs"], 0)

    def test_pipeline_defaults_to_vienna(self):
        from hyb2pipe import Options  # noqa: PLC0415
        self.assertEqual(Options().energy_engine, NEW_ENERGY_ENGINE)

    def test_desktop_samples_default_to_vienna(self):
        from desktop.runtime import sample_run  # noqa: PLC0415
        with tempfile.TemporaryDirectory() as tmp, no_hybrid_min(tmp):
            self.assertEqual(sample_run.energy_settings({})["energy_engine"], NEW_ENERGY_ENGINE)
            # a project saved before the replacement may still carry a hybrid-min path
            old = sample_run.energy_settings({"hybrid_min": "/opt/old/hybrid-min"})
            self.assertEqual(old["energy_engine"], NEW_ENERGY_ENGINE)

    def test_batch_energies_offer_vienna_not_hybrid_min(self):
        from desktop.runtime import batch_energy  # noqa: PLC0415
        self.assertIn(NEW_ENERGY_ENGINE, batch_energy.ENGINES)
        self.assertNotIn("hybrid-min", batch_energy.ENGINES)

    def test_no_hybrid_is_written_as_a_dot_not_100000(self):
        from hyb2pipe import energy  # noqa: PLC0415
        fasta = ">polyA\n" + "A" * 40 + "\n>rnaB\n" + SEQUENCE + "\n"
        rows = ["1_1\tAAAAAAAAAAAAAAAAAAAA\t.\tpolyA\t1\t10\t1\t10\t1e-5\tpolyA\t11\t20\t21\t30\t1e-5",
                "2_1\tGGGAAACCCGGGUUUCCC\t.\trnaB\t1\t9\t1\t9\t1e-5\trnaB\t10\t18\t20\t28\t1e-5"]
        with tempfile.TemporaryDirectory() as tmp, no_hybrid_min(tmp):
            src, dst = Path(tmp) / "in.hyb", Path(tmp) / "out.hyb"
            src.write_text("\n".join(rows) + "\n")
            # the path the pipeline takes for its default engine: each line's own two arms
            energy.add_line_energies_file(src, dst, fasta.splitlines(), energy.engine_folder(NEW_ENERGY_ENGINE, 1))
            out = [line.split("\t") for line in dst.read_text().splitlines()]
        self.assertEqual(out[0][2], ".")
        self.assertAlmostEqual(float(out[1][2]), reference_duplex_dg(SEQUENCE[0:9], SEQUENCE[19:28]), places=2)

    def test_pipeline_dg_column_on_the_test_data(self):
        from hyb2pipe import Options, awk, energy, sam2hyb  # noqa: PLC0415
        fasta = ROOT / "data/Zika_18S_formatted.fasta"
        with tempfile.TemporaryDirectory() as tmp, no_hybrid_min(tmp):
            result = sam2hyb(ROOT / "data/testData.sam", fasta, Path(tmp) / "out/testData", Options(energy=True))
            lines = result.hyb.read_text().splitlines()
        golden = (ROOT / "pipeline/tests/golden/testData.hyb").read_text().splitlines()
        self.assertEqual(len(lines), len(golden))
        # 1. only the dG column may change
        for number, (line, old) in enumerate(zip(lines, golden), 1):
            new_cols, old_cols = line.split("\t"), old.split("\t")
            self.assertEqual(new_cols[:2] + new_cols[3:], old_cols[:2] + old_cols[3:], f"line {number}")
        # 2. the run records ViennaRNA
        self.assertEqual(result.energy["engine"], NEW_ENERGY_ENGINE)
        self.assertIn("ViennaRNA", result.energy["program"])
        # 3. every dG is the RNAduplex dG of the line's two arms
        table = energy.reference_table(fasta.read_text().splitlines())
        new_dg = []
        for number, line in enumerate(lines, 1):
            pair = energy.bit_pair(line, table)
            got = line.split("\t")[2]
            self.assertIsNotNone(pair, f"line {number}")
            self.assertEqual(energy.pair_key(awk.fields(line)), line.split("\t")[0], f"line {number}")
            expected = reference_duplex_dg(pair[1], pair[2])
            if expected is None:
                self.assertEqual(got, ".", f"line {number}")
                new_dg.append(None)
            else:
                self.assertAlmostEqual(float(got), expected, places=2, msg=f"line {number}")
                new_dg.append(float(got))
        # 4. against hybrid-min, on the same lines (measured: r 0.978, mean -2.4 kcal/mol)
        old_dg = [None if row[2] == "." else float(row[2]) for row in
                  (line.split("\t") for line in (REFERENCE / "hybridmin_dg_testData.tsv").read_text().splitlines()
                   if not line.startswith("#"))]
        self.assertEqual(len(old_dg), len(new_dg))
        self.assertEqual([v is None for v in old_dg], [v is None for v in new_dg], "no-hybrid lines differ")
        both = [(a, b) for a, b in zip(old_dg, new_dg) if a is not None]
        self.assertGreater(statistics.correlation([a for a, _ in both], [b for _, b in both]), 0.95)
        self.assertTrue(-4.0 < statistics.mean(b - a for a, b in both) < 0.0)


# --------------------------------------------------------------------------
# ZIKV: the new C_ij against the cached hybrid-min C_ij of the genome folds
# --------------------------------------------------------------------------

ZIKV_JOBS = WORKSPACE / "kernel_experiments" / "jobs" / "zikv_new_b"
ZIKV_FASTA = WORKSPACE / "zika_files"
# windows and validated pairs in ZILM coordinates; ZAFR is ZILM - 1 past nt 72
WINDOWS = {"5'UTR-E": ((1, 81), (1052, 1171)), "NS2A-NS3": ((3851, 3921), (5703, 5770))}
VALIDATED = {"5'UTR-E": [(18, 1115), (29, 1100), (36, 1094)],
             "NS2A-NS3": [(3893, 5732), (3896, 5729), (3899, 5726)]}
# RNAduplex (ViennaRNA 2.7.2, RNA.duplexfold) on the cached arms, 2026-11-21:
# C between the windows, and C of the validated pairs
DUPLEX = {
    "ZILM": {"between": {"5'UTR-E": 9512, "NS2A-NS3": 166218},
             "validated": {"5'UTR-E": [238, 299, 291], "NS2A-NS3": [7198, 7206, 7196]}},
    "ZAFR": {"between": {"5'UTR-E": 114, "NS2A-NS3": 995},
             "validated": {"5'UTR-E": [0, 0, 0], "NS2A-NS3": [27, 27, 0]}},
}
# genome-wide (HYB2_SLOW=1), measured: ZILM q 1246 vs 1233, weighted Jaccard 0.67,
# hybrid-min mass covered 92%, top-n/3 overlap 0.89; ZAFR 843 vs 841, 0.62, 89%, 0.86
GENOME = {"q_ratio": (0.95, 1.05), "weighted_jaccard": 0.55, "mass": 0.85, "top_overlap": 0.80}


def _zafr(position):
    return position - 1 if position > 72 else position


def _overlaps(start, end, window):
    return start <= window[1] and end >= window[0]


class ZikvRegression(_Web):
    @classmethod
    def setUpClass(cls):
        super().setUpClass()
        if not ZIKV_JOBS.is_dir():
            raise unittest.SkipTest(f"cached ZIKV arms not found: {ZIKV_JOBS}")
        cls.data = {}
        for iso in ("ZILM", "ZAFR"):
            with open(ZIKV_FASTA / f"{iso}.fasta") as handle:
                sequence = "".join(line.strip() for line in handle
                                   if not line.startswith(">")).upper().replace("T", "U")
            with open(ZIKV_JOBS / "arms" / f"{iso}.json") as handle:
                blocks = Counter({tuple(row[:4]): row[4] for row in json.load(handle)})
            with open(ZIKV_JOBS / "pairs" / f"{iso}_hybridmin.json") as handle:
                pairs = json.load(handle)["pairs"]
            cls.data[iso] = (sequence, blocks, Counter({(i, j): n for i, j, n in pairs}))

    def _counts(self, sequence, blocks):
        return self.web.duplex_pair_counts(sequence, blocks)[0]

    def test_windows_and_validated_pairs(self):
        for iso, (sequence, blocks, hybrid_min) in self.data.items():
            shift = (lambda p: p) if iso == "ZILM" else _zafr
            for region, (x, y) in WINDOWS.items():
                x, y = (shift(x[0]), shift(x[1])), (shift(y[0]), shift(y[1]))
                near = Counter({b: n for b, n in blocks.items()
                                if (_overlaps(b[0], b[1], x) and _overlaps(b[2], b[3], y))
                                or (_overlaps(b[0], b[1], y) and _overlaps(b[2], b[3], x))})
                counts = self._counts(sequence, near)

                def between(table):
                    return sum(n for (i, j), n in table.items() if x[0] <= i <= x[1] and y[0] <= j <= y[1])
                with self.subTest(isolate=iso, region=region):
                    self.assertEqual(between(counts), DUPLEX[iso]["between"][region])
                    self.assertEqual([counts[(shift(i), shift(j))] for i, j in VALIDATED[region]],
                                     DUPLEX[iso]["validated"][region])
                    if iso == "ZILM":
                        ratio = between(counts) / between(hybrid_min)
                        self.assertTrue(0.85 <= ratio <= 1.15, f"between-window C / hybrid-min = {ratio:.3f}")

    @unittest.skipUnless(SLOW, "genome-wide comparison: set HYB2_SLOW=1 (about 25 minutes)")
    def test_genome_wide_counts_track_hybrid_min(self):
        for iso, (sequence, blocks, hybrid_min) in self.data.items():
            counts = self._counts(sequence, blocks)
            top = max(1, round(len(sequence) / 3))

            def q(table):
                return sum(n for _, n in table.most_common(top)) / top
            keys = set(counts) | set(hybrid_min)
            jaccard = (sum(min(counts[k], hybrid_min[k]) for k in keys)
                       / sum(max(counts[k], hybrid_min[k]) for k in keys))
            mass = sum(n for k, n in hybrid_min.items() if k in counts) / sum(hybrid_min.values())
            overlap = len({k for k, _ in counts.most_common(top)} & {k for k, _ in hybrid_min.most_common(top)}) / top
            with self.subTest(isolate=iso):
                low, high = GENOME["q_ratio"]
                self.assertTrue(low <= q(counts) / q(hybrid_min) <= high)
                self.assertGreaterEqual(jaccard, GENOME["weighted_jaccard"])
                self.assertGreaterEqual(mass, GENOME["mass"])
                self.assertGreaterEqual(overlap, GENOME["top_overlap"])
                self.assertTrue(math.isfinite(q(counts)))


if __name__ == "__main__":
    unittest.main()
