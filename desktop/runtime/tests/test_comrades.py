"""COMRADES on the yeast U2 snRNA: the source CLI (bin/comrades) and the
desktop structure task give the original hyb2_fold's structure, and the CLI
gives the desktop app's result. scripts/verify/verify-structure-worker.cjs
checks the web worker on the same data.

data/U2.fasta is the U2 snRNA (LSR1, 1,175 nt) and data/U2-U2.hyb.gz its
2,413 U2-U2 chimeras in the Brr2 data of Kudla et al. 2011 (PNAS 108:10010).
u2_hyb2_fold/ holds what the original hyb2_fold (bin/ of 5398ef3, with the
ViennaRNA 2.7.2 programs) gave for the whole RNA (hyb2_fold.*) and for
126-175 with 1126-1175 joined by its 100-nt spacer
(hyb2_fold_126-175_1126-1175.*):

    hyb2_fold -i U2-U2.hyb -a LSR1_LSR1_LSR1_snRNA -d U2.fasta -x 1 -l 1175 -r 1 -j ""
    hyb2_fold -i U2-U2.hyb -a LSR1_LSR1_LSR1_snRNA -d U2.fasta -x 126 -y 1126 -l 50 -r 1 -j ""

.vienna is RNAfold's structure and dG, .accepted the stem constraints that
comradesFold2 accepted (F, i, j, length), and .basepair_scores.txt the
RNAcofold pair counts of comradesMakeConstraints_2, each pair in the arm
order it was seen in.
"""
import json
import re
import shutil
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project

ROOT = Path(__file__).resolve().parents[3]
ORIGINAL = Path(__file__).resolve().parent / "u2_hyb2_fold"
GENE = "LSR1_LSR1_LSR1_snRNA"
DATA = ("U2.fasta", "U2-U2.hyb.gz")
WINDOW = (1081, 1160)  # stem IV
# a long-range layout: the two strands of stem V and stem IV
PAIRED = ((126, 175), (1126, 1175))


def _original(run="hyb2_fold"):
    """The original's structure, dG, accepted stems and pair counts (both arm orders summed)."""
    line = (ORIGINAL / f"{run}.vienna").read_text().splitlines()[2]
    structure, energy = re.fullmatch(r"(\S+)\s+\(\s*(\S+)\)", line).groups()
    stems = [[int(v) for v in row.split()[1:]] for row in (ORIGINAL / f"{run}.accepted").read_text().splitlines()]
    counts = {}
    for row in (ORIGINAL / f"{run}.basepair_scores.txt").read_text().splitlines():
        i, j, n = map(int, row.split())
        counts[(min(i, j), max(i, j))] = counts.get((min(i, j), max(i, j)), 0) + n
    return structure, float(energy), stems, counts


class U2Tests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.tmp = tempfile.TemporaryDirectory()
        root = Path(cls.tmp.name)
        # the CLI's source files alone, without the apps
        cls.public = root / "standalone"
        for name in (ROOT / "scripts/build/comrades-cli-files.txt").read_text().splitlines():
            source = (ROOT / name).resolve().relative_to(ROOT)
            assert source.parts[0] not in {"desktop", "web", ".git"}, source
            target = cls.public / source
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / source, target)
        cls.project = root / "u2.hyb2proj"
        new_project(cls.project, "U2")
        (cls.project / "data").mkdir()
        for name in DATA:
            shutil.copy2(ROOT / "data" / name, cls.public / name)
            shutil.copy2(ROOT / "data" / name, cls.project / "data" / name)

        whole = cls._cli("-o", "u2.json")
        assert whole.returncode == 0, whole.stderr
        cls.cli = json.loads((cls.public / "u2.json").read_text())
        cls.desktop = cls._desktop("u2")
        cls.cli_window = cls._cli_json("--start", str(WINDOW[0]), "--end", str(WINDOW[1]))
        cls.desktop_window = cls._desktop("u2-window", mode="windows", windows=[dict(zip(("start", "end"), WINDOW))])
        (one_start, one_end), (two_start, two_end) = PAIRED
        cls.cli_paired = cls._cli_json("--start", str(one_start), "--end", str(one_end), "--start-two", str(two_start),
                                       "--end-two", str(two_end), "--random-folds", "3", "--seed", "u2")
        cls.desktop_paired = cls._desktop("u2-paired", mode="paired", parameters={"random_folds": 3, "seed": "u2"},
                                          regions=[{"gene_id": GENE, "start": s, "end": e} for s, e in PAIRED])

    @classmethod
    def tearDownClass(cls):
        cls.tmp.cleanup()

    @classmethod
    def _cli(cls, *flags):
        args = [sys.executable, "-IB", "bin/comrades", "--reference", DATA[0], "--hyb", DATA[1], *flags]
        return subprocess.run(args, cwd=cls.public, capture_output=True, text=True)

    @classmethod
    def _cli_json(cls, *flags):
        run = cls._cli(*flags)
        assert run.returncode == 0, run.stderr
        return json.loads(run.stdout)

    @classmethod
    def _desktop(cls, task_id, **extra):
        """meta.json and the dot-bracket of a COMRADES structure task."""
        request = {"operation": "structure_run", "project_path": str(cls.project), "reference": "data/" + DATA[0],
                   "hyb_paths": ["data/" + DATA[1]], "gene_id": GENE, "engine": "comrades", "task_id": task_id, **extra}
        result = handle(request, repo_root=ROOT)
        assert result["ok"], result
        out = cls.project / "structures" / task_id
        return json.loads((out / "meta.json").read_text()), (out / "structure.dbn").read_text().splitlines()[2]

    def test_whole_rna_matches_the_original_hyb2_fold(self):
        structure, energy, stems, counts = _original()
        meta, dot = self.desktop
        for interface, folded, folded_energy, evidence in (
                ("bin/comrades", self.cli["structure"], self.cli["energy"], self.cli["evidence"]),
                ("desktop", dot, meta["records"][0]["energy"], meta["records"][0]["comrades"]["evidence"])):
            with self.subTest(interface=interface):
                self.assertEqual(folded, structure)
                self.assertEqual(folded_energy, energy)
                self.assertEqual([[c["left"], c["right"], c["length"]] for c in evidence["acceptedStemConstraints"]], stems)
                self.assertEqual({(p["one"], p["two"]): p["count"] for p in evidence["scoredEvidencePairs"]}, counts)

    def test_two_regions_match_the_original_hyb2_fold(self):
        # each chimera's arms cofolded in the .hyb's order, as hyb2_fold's;
        # region 1's arm first gave 139-1169 (11 bp) instead of 138-1170
        structure, energy, stems, counts = _original("hyb2_fold_126-175_1126-1175")
        meta, _ = self.desktop_paired
        self.assertEqual(self.cli_paired["structure"], structure)  # with the spacer, as the original's
        for interface, folded_energy, evidence in (
                ("bin/comrades", self.cli_paired["energy"], self.cli_paired["evidence"]),
                ("desktop", meta["records"][0]["energy"], meta["records"][0]["comrades"]["evidence"])):
            with self.subTest(interface=interface):
                self.assertEqual(folded_energy, energy)
                self.assertEqual([[c["left"], c["right"], c["length"]] for c in evidence["acceptedStemConstraints"]], stems)
                self.assertEqual({(p["one"], p["two"]): p["count"] for p in evidence["scoredEvidencePairs"]}, counts)

    def test_cli_gives_the_desktop_result(self):
        for name, cli, (meta, dot) in (("whole RNA", self.cli, self.desktop), ("window", self.cli_window, self.desktop_window),
                                       ("two regions", self.cli_paired, self.desktop_paired)):
            record = meta["records"][0]
            with self.subTest(name):
                self.assertEqual(cli["parameters"], {key: meta["parameters"][key] for key in cli["parameters"]})
                self.assertEqual(cli["energy"], record["energy"])
                self.assertEqual(cli["evidence"], record["comrades"]["evidence"])
                self.assertEqual(cli["randomisation"], record["comrades"]["randomisation"])
                # the desktop stores two regions as two strands, without the spacer
                self.assertEqual(cli.get("withoutSpacer", cli)["structure"], dot)
        self.assertEqual(len(self.cli_paired["randomisation"]["structures"]), 4)
        self.assertEqual([(r["start"], r["end"]) for r in self.cli_paired["regions"]], list(PAIRED))

    def test_cli_rejects_bad_input(self):
        for flags, message in ((["--constraint-limit", "0"], "Evidence stem constraints must be a whole number from 1 to 75"),
                               (["--random-folds", "1001"], "Randomised folds must be a whole number from 0 to 1000"),
                               (["--start", "300", "--end", "400"], f"no {GENE} chimeras have both arms inside 300-400"),
                               (["--start-two", "1100", "--end-two", "1200"], "region 2 (1100-1200) is outside 1-1175")):
            with self.subTest(flags=flags):
                bad = self._cli(*flags)
                self.assertEqual((bad.returncode, bad.stdout), (1, ""))
                self.assertIn(message, bad.stderr)


if __name__ == "__main__":
    unittest.main()
