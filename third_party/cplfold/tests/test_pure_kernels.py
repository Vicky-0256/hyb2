"""cplfold/_internal/pure_kernels.py must stay bit-identical to the Numba kernels' Python.

Without Numba (the browser's Pyodide runtime) cplfold/_internal/parser.py swaps
``parse_loop_vienna`` and ``_outside_kernel`` for the list-based versions in
cplfold/_internal/pure_kernels.py.  They are a transcription of the decorated kernels, so
a change to either kernel in parser.py has to be made there too.

The test runs the same work with the original kernels (CPLFOLD_PURE_KERNELS=0)
and with the pure ones, each in a fresh interpreter with an identity ``numba``
stub.  It compares the raw bytes of every parser state array (inside, outside
and C arrays) after each ``parse`` and ``parse_subopt``, not only the final
structures: small beams, ties, every alpha (0 included) with a nonzero bonus
matrix, tiny sequences and forced-pair constraints exercise the pruning and
boundary rules that a few full folds leave untouched.  The full two-phase
folds are compared as ``repr`` of every float.

When Numba is installed, a third run enables JIT on a small subset and
requires nopython signatures on both kernels after execution.
``parse_loop_vienna`` is compiled with fastmath=True, so it
may differ from Python by a few ulps: that run compares structures and
energies rounded to 1e-6, not bytes.
"""

import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest
from importlib.machinery import PathFinder
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

RUNNER = textwrap.dedent(r'''
    import hashlib, io, contextlib, json, os, random, sys, types
    import numpy as np

    MODE = sys.argv[2]                        # "stub": identity numba; "numba": the real JIT
    if MODE == "stub":
        stub = types.ModuleType("numba")      # no __version__: as in Pyodide
        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function
        stub.njit = njit
        sys.modules["numba"] = stub
    else:
        import numba
        from numba.core.registry import CPUDispatcher
        if numba.config.DISABLE_JIT:
            raise AssertionError("Native parity requires an enabled Numba JIT")
    sys.path.insert(0, sys.argv[1])
    from cplfold._internal import parser as parser_module
    from cplfold._internal.parser import BeamCKYParserHyper
    from cplfold._internal.single import two_phase_pseudoknot_fold

    native_kernels = {}
    pure = MODE == "stub" and os.environ.get("CPLFOLD_PURE_KERNELS") == "1"
    expected_module = "cplfold._internal.pure_kernels" if pure else "cplfold._internal.parser"
    for name in ("parse_loop_vienna", "_outside_kernel"):
        kernel = getattr(parser_module, name)
        if kernel.__module__ != expected_module:
            raise AssertionError(f"{name} resolved to {kernel.__module__}, expected {expected_module}")
        if MODE == "numba":
            if not isinstance(kernel, CPUDispatcher):
                raise AssertionError(f"{name} is not a Numba CPU dispatcher")
            native_kernels[name] = kernel

    STATE = ("_scores", "_manners", "_splits", "_l1s", "_l2s", "_c_scores", "_c_manners", "_c_splits",
             "_beta_scores", "_beta_manners", "_beta_splits", "_beta_l1s", "_beta_l2s",
             "_beta_c_scores", "_beta_c_manners", "_beta_c_splits")
    PAIRS = {"AU", "UA", "GC", "CG", "GU", "UG"}

    def floats(value):
        if isinstance(value, float):
            return repr(value)
        if isinstance(value, dict):
            return {str(key): floats(item) for key, item in value.items()}
        if isinstance(value, (list, tuple)):
            return [floats(item) for item in value]
        if isinstance(value, np.ndarray):
            return hashlib.sha256(np.ascontiguousarray(value).tobytes()).hexdigest()
        if isinstance(value, np.generic):
            return repr(value.item())
        return value

    def coarse(value):
        # fastmath-tolerant view: strings and integers as they are, floats to
        # 1e-6, arrays left out (their bytes may differ by ulps).
        if isinstance(value, (float, np.floating)):
            return round(float(value), 6) + 0.0
        if isinstance(value, dict):
            return {str(key): coarse(item) for key, item in value.items() if not isinstance(item, np.ndarray)}
        if isinstance(value, (list, tuple)):
            return [coarse(item) for item in value if not isinstance(item, np.ndarray)]
        if isinstance(value, np.generic):
            return value.item()
        return value

    def state(parser):
        if MODE != "stub":
            return {}
        return {name: hashlib.sha256(np.ascontiguousarray(getattr(parser, name)).tobytes()).hexdigest()
                for name in STATE if isinstance(getattr(parser, name, None), np.ndarray)}

    def record(out, result):
        # bytes-exact view (stub runs only) and the rounded one (every run)
        if MODE == "stub":
            out["steps"].append([floats(result), state(out["parser"])])
        out["coarse"].append(coarse(result))

    rng = random.Random(11)

    def sequence(n, kind):
        if kind == "repeat":                  # many tied scores
            unit = "".join(rng.choice("ACGU") for _ in range(rng.randint(1, 4)))
            return (unit * n)[:n]
        if kind == "gc":
            return "".join(rng.choice("GC") for _ in range(n))
        return "".join(rng.choice("ACGU") for _ in range(n))

    def constraint(seq):
        """Up to three nested or disjoint forced pairs plus some forced-unpaired bases."""
        marks = ["?"] * len(seq)
        for _ in range(3):
            i = rng.randrange(len(seq))
            for j in range(min(len(seq) - 1, i + 30), i + 3, -1):
                if seq[i] + seq[j] in PAIRS and all(mark == "?" for mark in marks[i:j + 1]):
                    marks[i], marks[j] = "(", ")"
                    break
        for _ in range(len(seq) // 6):
            k = rng.randrange(len(seq))
            if marks[k] == "?":
                marks[k] = "."
        return "".join(marks)

    raw = []
    lengths = [1, 2, 4, 6, 9, 12] + [rng.randint(20, 70) for _ in range(30)]
    # The numba run JIT-compiles every kernel first: keep its share small.
    subset = range(len(lengths)) if MODE == "stub" else (7, 11, 15, 16, 17, 21)
    for index, n in enumerate(lengths):
        kind = ("random", "repeat", "gc")[index % 3]
        seq = sequence(n, kind)
        beam = (1, 2, 3, 5, 20, 0)[index % 6]
        constrained = index % 4 == 3 and n > 8
        cons = None
        if constrained:
            # A constrained case must force at least one pair: a sequence
            # with no pairable bases (e.g. poly-C) is replaced.
            for _attempt in range(50):
                cons = constraint(seq)
                if "(" in cons:
                    break
                kind = "random"
                seq = sequence(n, kind)
            else:
                raise AssertionError("no forced pair placed for case %d" % index)
        parser = BeamCKYParserHyper(beam_size=beam, nosharpturn=True, lv=True, use_constraints=constrained)
        alpha = None
        if index % 2:
            bonus = np.zeros((n, n))
            ties = index % 5 == 0
            for _ in range(2 * n):
                i, j = sorted(rng.sample(range(n), 2)) if n > 1 else (0, 0)
                if j - i > 3:
                    bonus[i, j] = bonus[j, i] = rng.choice((0.5, 1.0)) if ties else rng.random() * 3
            # Independent of index % 4, so alpha 0 and 1.0 meet a nonzero
            # bonus matrix and the constrained cases (index % 4 == 3) vary too.
            alpha = (0.0, 0.5, 1.0, 2.5)[(index // 4) % 4]
            parser.set_alpha(alpha)
            parser.set_bonus_matrix(bonus, n)
        if index not in subset:
            continue
        out = {"index": index, "n": n, "beam": beam, "kind": kind, "alpha": alpha,
               "forced": cons.count("(") if cons else 0, "steps": [], "coarse": [], "parser": parser}
        with contextlib.redirect_stdout(io.StringIO()):
            if constrained:
                result = parser.parse(seq, cons=cons)
                record(out, result[:3] if len(result) == 4 else result)
            result = parser.parse(seq, cons=None)
            record(out, result[:3] if len(result) == 4 else result)
            if n > 1:
                result = parser.parse_subopt(seq, energy_delta=(1.0, 5.0, 10.0)[index % 3], max_structures=10)
                record(out, result)
                # A misspelt or missing STATE name would silently drop out.
                if MODE == "stub" and sorted(out["steps"][-1][1]) != sorted(STATE):
                    raise AssertionError("parser state arrays %s != STATE %s"
                                         % (sorted(out["steps"][-1][1]), sorted(STATE)))
        del out["parser"]
        raw.append(out)

    folds, folds_coarse = [], []
    nrng = np.random.default_rng(7)
    for length, alpha, beta, pseudoknot, max_phase1, delta in (
            (90, 0.5, 0.0, True, 3, 5.0), (120, 1.0, 0.3, True, 4, 8.0), (110, 0.0, 0.0, False, 3, 5.0)):
        seq = "".join(nrng.choice(list("ACGU"), length))
        bonus = np.zeros((length, length), dtype=np.float32)
        for _ in range(length):
            i, j = sorted(nrng.integers(0, length, 2))
            if j - i > 4:
                bonus[i, j] = bonus[j, i] = nrng.random() * 3
        if MODE != "stub" and length == 120:   # after the draws: same sequences
            continue
        fold = two_phase_pseudoknot_fold(seq, beam_size=20, energy_delta=delta, max_phase1=max_phase1,
                                         max_phase2=3, energy_model="DP09", verbose=False,
                                         bonus_matrix=bonus, alpha=alpha, beta=beta,
                                         allow_pseudoknot=pseudoknot)
        if MODE == "stub":
            folds.append(floats(fold))
        folds_coarse.append([length, coarse(fold)])
    compiled = {}
    for name, kernel in native_kernels.items():
        if not kernel.nopython_signatures:
            raise AssertionError(f"{name} did not execute a nopython signature during parity checks")
        compiled[name] = len(kernel.nopython_signatures)
    print(json.dumps({"raw": raw, "folds": folds, "foldsCoarse": folds_coarse,
                      "compiled": compiled}, sort_keys=True))
''')


# Numba's on-disk cache (the kernels use cache=True) goes outside the source
# tree; it keeps the JIT run's ~100 s compile to the first run on a machine.
NUMBA_CACHE = Path(tempfile.gettempdir()) / "cplfold-test-pure-kernels-numba-cache"


def _start(pure: bool, root: Path = ROOT, mode: str = "stub") -> subprocess.Popen:
    env = dict(os.environ, CPLFOLD_PURE_KERNELS="1" if pure else "0", PYTHONDONTWRITEBYTECODE="1",
               NUMBA_CACHE_DIR=os.environ.get("NUMBA_CACHE_DIR") or str(NUMBA_CACHE))
    env.pop("CPLFOLD_PYTHON_OUTSIDE", None)
    if mode == "numba":
        env["NUMBA_DISABLE_JIT"] = "0"
    # Output to files, not pipes: a full pipe would stall a run started in
    # parallel until the one before it is read.
    out, err = tempfile.TemporaryFile("w+"), tempfile.TemporaryFile("w+")
    process = subprocess.Popen([sys.executable, "-c", RUNNER, str(root), mode], env=env, stdout=out, stderr=err)
    process.output_files = (out, err)
    return process


def _finish(process: subprocess.Popen) -> dict:
    process.wait(timeout=1800)
    out, err = process.output_files
    out.seek(0), err.seek(0)
    stdout, stderr = out.read(), err.read()
    out.close(), err.close()
    if process.returncode != 0:
        raise AssertionError(stderr[-3000:])
    return json.loads(stdout.strip().splitlines()[-1])


def _run(pure: bool, root: Path = ROOT, mode: str = "stub") -> dict:
    return _finish(_start(pure, root, mode))


def _all_runs() -> dict:
    """The three runs, started together (they are independent processes)."""
    processes = {"original": _start(False), "pure": _start(True)}
    # Other test modules may have installed an identity stub in sys.modules.
    # An installed but broken Numba must report its import error in the child.
    if PathFinder.find_spec("numba") is not None:
        processes["numba"] = _start(False, ROOT, "numba")
    try:
        return {name: _finish(process) for name, process in processes.items()}
    finally:
        for process in processes.values():
            if process.poll() is None:
                process.kill()


class PureKernelsMatchTheNumbaKernels(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.runs = _all_runs()

    def test_same_parser_state_and_folds(self):
        original, pure = self.runs["original"], self.runs["pure"]
        self.assertTrue(all(original["folds"]), "every fold must give structures")
        self.assertEqual(len(original["raw"]), len(pure["raw"]))
        for before, after in zip(original["raw"], pure["raw"]):
            self.assertEqual(before, after, f"parser state differs (n={before['n']}, beam={before['beam']})")
        self.assertEqual(original["folds"], pure["folds"])
        # Coverage the cases are meant to have.
        alphas = {case["alpha"] for case in original["raw"]}
        self.assertTrue({0.0, 0.5, 1.0, 2.5} <= alphas, alphas)
        constrained = [case for case in original["raw"] if case["forced"]]
        self.assertEqual(len(constrained), sum(1 for case in original["raw"]
                                               if case["index"] % 4 == 3 and case["n"] > 8))
        self.assertGreaterEqual(len(constrained), 5)
        self.assertGreater(len({case["alpha"] for case in constrained}), 1)

    def test_numba_jit_kernels_agree(self):
        if "numba" not in self.runs:
            self.skipTest("Numba is not installed")
        jit, pure = self.runs["numba"], self.runs["pure"]
        by_index = {case["index"]: case for case in pure["raw"]}
        self.assertTrue(jit["raw"])
        for case in jit["raw"]:
            self.assertEqual(case["coarse"], by_index[case["index"]]["coarse"],
                             f"numba differs from Python (case {case['index']}, n={case['n']}, beam={case['beam']})")
        by_length = dict((length, fold) for length, fold in pure["foldsCoarse"])
        self.assertTrue(jit["foldsCoarse"])
        for length, fold in jit["foldsCoarse"]:
            self.assertEqual(fold, by_length[length], f"numba two-phase fold differs (length {length})")
        signature_counts = ", ".join(f"{name}={count}" for name, count in jit["compiled"].items())
        print(f"CPLfold nopython parity passed ({signature_counts})")


if __name__ == "__main__":
    unittest.main()
