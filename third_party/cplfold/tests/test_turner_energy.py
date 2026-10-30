"""Turner 2004 ranking of pseudoknot-free CPLfold runs."""

import os
import sys
import unittest

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, ROOT)

from CPLfold import two_phase_pseudoknot_fold  # noqa: E402
from Utils.hotknots_energy import HotKnotsEnergy  # noqa: E402
from Utils.turner_energy import turner_energy  # noqa: E402

# ViennaRNA 2.7.2 eval_structure (Turner 2004, dangles=2), rounded to 0.01 kcal/mol.
VIENNA = [
    ('CUUGUCUCCAAG', '((((....))))', -0.5),
    ('UACCCAUUUAGUAGACAAAUCGUUC', '.............(((.....))).', 0.5),
    ('CAUCACCAAUUCGCUGGUUGUUGAACUAUACGACCGGGGC', '...........(.((((((((........)))))))).).', -12.0),
    ('CAUCACCAAUUCGCUGGUUGUUGAACUAUACGACCGGGGC', '.............((((((((........))))))))...', -10.7),
    ('ACACUGCACUCAGUUCCCAUUUAGAGGAUCCUAGCCUAGCUACGCGUUUGCGCAUCAGGC',
     '..((((....))))(((........))).....((((.(...(((....)))...)))))', -9.9),
    ('UGUCCCAUACAUCAAGCGGUUCCCCUCAAAUUAUCCGGACUCGGUAAGGGCAGCGAGUAAAUAUUUUACAAUACGUUUCUUGUCAAUCUG',
     '........(((..((((((((.((((.....((.(((....))))))))).)))..(((((...)))))....)))))..))).......', -13.4),
    ('CUGCUUUGUACGCGUCACAGUUACUCGGCGAAGGCCCGUCUUUUUGCUGACCAGGAAAUUUCACAGCUGAGCCUAGCUUCCUAAAUCCAUUUGCG'
     'CGGGAAACACGGGACAUGUCAACGGUCCUAGCCAG',
     '.....(((.(((.(((...(((.(((((((((((...(((........))).((((........(((((....)))))))))....)).)))))'
     '.)))).)))....))).)))))).(((....)))..', -27.6),
    ('GGGGAAACCCC', '((((...))))', -4.5),
    ('GCGCUUCGGCGCAAAAGGGAAACCC', '((((....))))....(((...)))', -8.6),
    ('GGGCGCAAGCCU', '((((....))))', -5.0),
]


class TurnerEnergyTests(unittest.TestCase):
    def test_matches_viennarna(self):
        for sequence, structure, expected in VIENNA:
            with self.subTest(structure=structure):
                self.assertAlmostEqual(turner_energy(sequence, structure), expected, places=2)

    def test_rejects_crossing_pairs(self):
        with self.assertRaises(ValueError):
            turner_energy('GGGAAACCCAGGGAAACCCU', '(((..[[[)))....]]]..')

    def test_pseudoknot_free_run_ranks_with_turner(self):
        sequence = VIENNA[4][0]
        results = two_phase_pseudoknot_fold(sequence, beam_size=20, max_phase1=5, energy_delta=5.0,
                                            verbose=False, allow_pseudoknot=False)
        energies = [r['energy'] for r in results]
        for r in results:
            self.assertAlmostEqual(r['energy'], turner_energy(sequence, r['structure']), places=6)
        self.assertEqual(energies, sorted(energies))

    def test_pseudoknot_run_ranks_with_the_requested_model(self):
        sequence = VIENNA[4][0]
        results = two_phase_pseudoknot_fold(sequence, beam_size=20, max_phase1=3, verbose=False,
                                            allow_pseudoknot=True, energy_model="DP09")
        hk = HotKnotsEnergy()
        for r in results:
            self.assertAlmostEqual(r['energy'], hk.compute_energy(sequence, r['structure'], 'DP09')['energy'], places=6)


if __name__ == '__main__':
    unittest.main()
