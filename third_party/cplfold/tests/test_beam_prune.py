"""Pins CPLfold's beam-pruning rule to LinearFold's ``beam_prune``.

LinearFold (src/LinearFold.cpp) computes

    threshold = quickselect(scores, 0, scores.size() - 1, scores.size() - beam);
    for (auto &p : scores) if (p.first < threshold) beamstep.erase(p.second);

``quickselect``'s last argument is a 1-based rank in ascending order, so the
threshold is the (size - beam)-th smallest score, i.e. the (beam + 1)-th best.
Candidates strictly below it are erased: beam + 1 candidates survive when the
scores are distinct, and every candidate tied with the threshold survives too.
The call sites prune only when ``beam > 0`` and the set holds more than ``beam``
candidates.

Each score is ``bestC[i - 1].score + cand.score``, except

    if ((k >= 0) && (bestC[k].score == VALUE_MIN)) newscore = VALUE_MIN;

so a candidate whose exterior prefix is unreachable (possible only under
forced-pair constraints) ranks last, tied with every other such candidate.
"""

import importlib
import random
import sys
import types
import unittest

import numpy as np


def _load_parser_with_numba_stub():
    """Import cplfold._internal.parser without paying the JIT startup cost."""

    if "numba" not in sys.modules:
        numba = types.ModuleType("numba")

        def njit(*args, **kwargs):
            if len(args) == 1 and callable(args[0]) and not kwargs:
                return args[0]
            return lambda function: function

        numba.njit = njit
        sys.modules["numba"] = numba
    return importlib.import_module("cplfold._internal.parser")


def _linearfold_survivors(scores, beam):
    """Indices LinearFold keeps, from the definition above (no shared code)."""

    if beam <= 0 or len(scores) <= beam:
        return list(range(len(scores)))
    threshold = sorted(scores)[len(scores) - beam - 1]  # 1-based rank size - beam
    return [index for index, score in enumerate(scores) if not score < threshold]


class BeamPruneRuleTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.parser = _load_parser_with_numba_stub()

    def prune(self, scores, beam):
        count = len(scores)
        # Buffers are longer than the candidate set, as in the parse loops.
        positions = np.full(count + 3, -1, dtype=np.int32)
        positions[:count] = np.arange(count, dtype=np.int32)
        values = np.full(count + 3, np.nan, dtype=np.float64)
        values[:count] = scores
        kept = int(self.parser.beam_prune_deterministic(positions, values, count, beam))
        self.assertLessEqual(kept, count)
        survivors = [int(position) for position in positions[:kept]]
        self.assertEqual([float(value) for value in values[:kept]],
                         [float(scores[index]) for index in survivors])
        return survivors

    def test_distinct_scores_keep_beam_plus_one(self):
        self.assertEqual(self.prune([50, 40, 30, 20, 10], 2), [0, 1, 2])
        self.assertEqual(self.prune([10, 50, 20, 40, 30], 2), [1, 3, 4])
        self.assertEqual(self.prune([5, 4, 3], 1), [0, 1])
        for count in range(2, 40):
            scores = [float(value) for value in random.Random(count).sample(range(1000), count)]
            for beam in range(1, count):
                self.assertEqual(len(self.prune(scores, beam)), beam + 1, (count, beam))

    def test_ties_at_the_threshold_all_survive(self):
        # threshold is the third best score (30); its three holders all stay
        self.assertEqual(self.prune([50, 40, 30, 30, 30, 10], 2), [0, 1, 2, 3, 4])
        self.assertEqual(self.prune([30, 10, 30, 50, 30, 40], 2), [0, 2, 3, 4, 5])
        self.assertEqual(self.prune([7, 7, 7, 7], 1), [0, 1, 2, 3])

    def test_ties_above_the_threshold_do_not_widen_the_beam(self):
        # the third best score is the second 40, so 30 is strictly below it
        self.assertEqual(self.prune([50, 40, 40, 30, 20], 2), [0, 1, 2])
        self.assertEqual(self.prune([50, 50, 50, 30, 20], 2), [0, 1, 2])

    def test_no_pruning_at_or_below_the_beam(self):
        self.assertEqual(self.prune([3, 2, 1], 3), [0, 1, 2])
        self.assertEqual(self.prune([3, 2, 1], 5), [0, 1, 2])
        self.assertEqual(self.prune([], 2), [])

    def test_one_candidate_over_the_beam_is_not_pruned(self):
        # size == beam + 1: the threshold is the lowest score, nothing is below it
        self.assertEqual(self.prune([4, 1, 3, 2], 3), [0, 1, 2, 3])

    def test_beam_zero_disables_pruning(self):
        self.assertEqual(self.prune([50, 40, 30, 20, 10], 0), [0, 1, 2, 3, 4])

    def test_matches_linearfold_rule_on_random_candidate_sets(self):
        rng = random.Random(20261115)
        for _ in range(2000):
            count = rng.randint(1, 30)
            spread = rng.choice((2, 5, 1000))  # heavy ties, some ties, mostly distinct
            scores = [float(rng.randint(-spread, spread)) for _ in range(count)]
            beam = rng.randint(0, count + 1)
            self.assertEqual(self.prune(scores, beam), _linearfold_survivors(scores, beam),
                             (scores, beam))


class BeamPruneScoreTests(unittest.TestCase):
    """The score a candidate is ranked by, including LinearFold's VALUE_MIN case."""

    @classmethod
    def setUpClass(cls):
        cls.parser = _load_parser_with_numba_stub()

    def test_reachable_prefix_is_added(self):
        score = self.parser.beam_prune_score
        self.assertEqual(float(score(np.float64(0.0), np.float64(-310.0))), -310.0)
        self.assertEqual(float(score(np.float64(-1250.0), np.float64(430.0))), -820.0)
        self.assertEqual(float(score(np.float64(12.5), np.float64(0.25))), 12.75)

    def test_unreachable_prefix_ranks_last_whatever_the_candidate_scores(self):
        score = self.parser.beam_prune_score
        value_min = float(self.parser.VALUE_MIN_FLOAT)
        # The sentinel plus a score is not the sentinel once the score reaches the
        # float spacing at 1e18, which is what used to order these candidates.
        self.assertNotEqual(value_min + 5000.0, value_min + 100.0)
        for candidate in (-5000.0, -100.0, 0.0, 100.0, 5000.0, 1e6):
            self.assertEqual(float(score(np.float64(value_min), np.float64(candidate))), value_min)
        # C = C + P can leave sentinel + score in C; that prefix is unreachable too.
        self.assertEqual(float(score(np.float64(value_min + 4096.0), np.float64(300.0))), value_min)

    def test_candidates_behind_an_unreachable_prefix_are_not_pruned_against_each_other(self):
        value_min = self.parser.VALUE_MIN_FLOAT
        prefixes = [np.float64(0.0), value_min, value_min, value_min, value_min]
        candidates = [-200.0, 5000.0, 100.0, -3000.0, 900.0]
        count = len(candidates)
        positions = np.arange(count, dtype=np.int32)
        values = np.array([self.parser.beam_prune_score(prefix, np.float64(candidate))
                           for prefix, candidate in zip(prefixes, candidates)], dtype=np.float64)
        for beam in (1, 2, 3):
            kept_positions, kept_values = positions.copy(), values.copy()
            kept = int(self.parser.beam_prune_deterministic(kept_positions, kept_values, count, beam))
            self.assertEqual([int(position) for position in kept_positions[:kept]],
                             _linearfold_survivors([float(value) for value in values], beam))
            self.assertEqual(kept, count, beam)

    def test_forced_outer_pair_makes_any_beam_exhaustive(self):
        # With (0, n - 1) forced, bestC[k] is unreachable for every k < n - 1, so each
        # candidate with i >= 1 scores VALUE_MIN, the threshold is VALUE_MIN and nothing
        # is erased: LinearFold folds such an input identically at every beam.
        cases = (
            (True, "GGUGUGAAUCGCUUAAGGGUUAAGUAAGUGUGAUC"),
            (False, "GGUGUGAAUCGCUUAAGGGUUAAGUAAGUGUGAUC"),
        )
        for vienna, sequence in cases:
            constraint = "(" + "?" * (len(sequence) - 2) + ")"
            exhaustive = self.parser.BeamCKYParserHyper(
                beam_size=0, lv=vienna, use_constraints=True).parse(sequence, cons=constraint)
            self.assertEqual((exhaustive[0][0], exhaustive[0][-1]), ("(", ")"))
            for beam in (1, 2):
                pruned = self.parser.BeamCKYParserHyper(
                    beam_size=beam, lv=vienna, use_constraints=True).parse(sequence, cons=constraint)
                self.assertEqual(pruned[0], exhaustive[0], (vienna, beam))
                self.assertAlmostEqual(pruned[1], exhaustive[1], places=9, msg=str((vienna, beam)))


class BeamPruneBufferTests(unittest.TestCase):
    """More than ``beam`` survivors must fit every per-position buffer."""

    @classmethod
    def setUpClass(cls):
        cls.parser = _load_parser_with_numba_stub()

    def test_small_beams_fold_tied_candidates_without_overflow(self):
        # Without the JIT (the numba stub, or NUMBA_DISABLE_JIT=1) NumPy checks
        # every index, so a buffer sized for exactly ``beam`` survivors raises.
        sequence = "GGGGAAAACCCCAAGGGGAAAACCCC"
        for vienna in (True, False):
            for beam in (1, 2, 3):
                parser = self.parser.BeamCKYParserHyper(beam_size=beam, lv=vienna)
                structure = parser.parse(sequence)[0]
                self.assertEqual(len(structure), len(sequence))
                self.assertEqual(structure.count("("), structure.count(")"))


if __name__ == "__main__":
    unittest.main()
