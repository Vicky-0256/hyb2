"""Regression tests for CPLfold command-line exit semantics."""

import os
import sys
import unittest
from unittest import mock

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if ROOT not in sys.path:
    sys.path.insert(0, ROOT)

import CPLfold  # noqa: E402


class CPLfoldCliExitStatusTests(unittest.TestCase):
    def test_phase1_only_result_is_successful(self):
        result = {"type": "phase1"}
        with mock.patch.object(
            CPLfold, "two_phase_pseudoknot_fold", return_value=[result]
        ), mock.patch.object(sys, "argv", ["CPLfold.py", "-s", "GAAAC", "-q"]):
            self.assertEqual(CPLfold.main(), 0)

    def test_empty_result_remains_unsuccessful(self):
        with mock.patch.object(
            CPLfold, "two_phase_pseudoknot_fold", return_value=[]
        ), mock.patch.object(sys, "argv", ["CPLfold.py", "-s", "GAAAC", "-q"]):
            self.assertEqual(CPLfold.main(), 1)


if __name__ == "__main__":
    unittest.main()
