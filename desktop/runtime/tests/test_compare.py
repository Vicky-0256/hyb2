import json
import random
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import load_manifest, new_project, register_task_run, validate_project_directory

ROOT = Path(__file__).resolve().parents[3]
GENE = "TEST_virusRNA"
LENGTH = 2000


def _rows(seed, enriched):
    """Random intramolecular hybrids; ``enriched`` adds a block at 300x1500."""
    rng = random.Random(seed)
    rows = []
    for k in range(3000):
        a0 = rng.randint(1, LENGTH - 60)
        b0 = rng.randint(1, LENGTH - 60)
        rows.append((a0, a0 + rng.randint(15, 40), b0, b0 + rng.randint(15, 40)))
    for _ in range(enriched):
        a0 = 300 + rng.randint(0, 20)
        b0 = 1500 + rng.randint(0, 20)
        rows.append((a0, a0 + 25, b0, b0 + 25))
    return "".join(f"{k}_1\tACGU\t.\t{GENE}\t1\t20\t{a0}\t{a1}\t1e-5\t{GENE}\t21\t40\t{b0}\t{b1}\t1e-5\t\t-10\tType_1\n"
                   for k, (a0, a1, b0, b1) in enumerate(rows))


class CompareRunTests(unittest.TestCase):
    def setUp(self):
        from desktop.runtime.samples import add_reference, add_sample

        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.project = base / "p.hyb2proj"
        new_project(self.project, "Compare")
        ref = base / "ref.fa"
        ref.write_text(f">{GENE}\n" + "ACGU" * (LENGTH // 4) + "\n")
        reference = add_reference(self.project, ref, mode="linked")["reference"]
        self.ids = []
        for name, seed, enriched in (("a1", 1, 300), ("a2", 2, 280), ("a3", 5, 320), ("b1", 3, 0), ("b2", 4, 0)):
            path = base / f"{name}.hyb"
            path.write_text(_rows(seed, enriched))
            sample = add_sample(self.project, path, name=name, reference_id=reference["id"], gene_id=GENE,
                                condition=name[0], mode="linked")
            self.ids.append(sample["id"])

    def tearDown(self):
        self.tmp.cleanup()

    def _request(self, **extra):
        request = {"operation": "compare_run", "project_path": str(self.project), "task_id": "cmp1",
                   "samples_one": self.ids[:3], "samples_two": self.ids[3:],
                   "parameters": {"label_one": "A", "label_two": "B"}}
        request.update(extra)
        return request

    def test_run_register_list_and_read(self):
        result = handle(self._request(), repo_root=ROOT)
        self.assertTrue(result["ok"], result)
        out = self.project / "comparisons" / "cmp1"
        for name in ("results.tsv", "significant.tsv", "interactions.tsv", "summary.json"):
            self.assertTrue((out / name).is_file(), name)
        # linked .hyb samples have no contact.txt: the comparison makes them
        self.assertEqual(len(list((out / "contacts").glob("*.contact.txt"))), 5)
        summary = json.loads((out / "summary.json").read_text())
        self.assertEqual(summary["label_one"], "A")
        self.assertGreater(summary["significant_up"], 0)
        self.assertGreater(summary["interactions_up"], 0)
        interactions = (out / "interactions.tsv").read_text().splitlines()
        top = interactions[1].split("\t")
        self.assertEqual(top[0], "up")
        self.assertTrue(280 <= int(top[1]) <= 350 and 1480 <= int(top[2]) <= 1550, top)

        # what the Rust host does when the task ends
        run_dir = Path(result["run_dir"])
        (run_dir / "status.json").write_text(json.dumps({"task_id": "cmp1", "kind": "compare", "status": "succeeded"}))
        register_task_run(self.project, "cmp1")
        manifest = load_manifest(self.project)
        entry = manifest["comparisons"][0]
        self.assertEqual(entry["id"], "cmp1")
        self.assertEqual(entry["samples_one"], self.ids[:3])
        self.assertEqual(entry["paths"]["significant"], "comparisons/cmp1/significant.tsv")
        self.assertEqual(validate_project_directory(self.project), [])

        listed = handle({"operation": "list_comparisons", "path": str(self.project)})
        self.assertEqual([item["id"] for item in listed["comparisons"]], ["cmp1"])
        self.assertTrue(listed["comparisons"][0]["available"])
        read = handle({"operation": "read_comparison", "path": str(self.project), "comparison_id": "cmp1", "limit": 5})
        self.assertEqual(len(read["cells"]["x"]), min(5, summary["significant"]))
        self.assertEqual(read["truncated"], summary["significant"] > 5)
        self.assertEqual(len(read["interactions"]), summary["interactions"])
        with self.assertRaisesRegex(ValueError, "does not exist"):
            handle({"operation": "read_comparison", "path": str(self.project), "comparison_id": "nope"})

    def test_rejects_bad_requests(self):
        with self.assertRaisesRegex(ValueError, "both conditions"):
            handle(self._request(samples_two=[self.ids[0], self.ids[3]]), repo_root=ROOT)
        with self.assertRaisesRegex(ValueError, "at least three"):
            handle(self._request(samples_one=[self.ids[0]], samples_two=[self.ids[3]]), repo_root=ROOT)
        with self.assertRaisesRegex(ValueError, "unknown compare parameters"):
            handle(self._request(parameters={"alpha": 0.1}), repo_root=ROOT)
        result = handle(self._request(samples_two=[self.ids[3], "missing"], task_id="cmp2"), repo_root=ROOT)
        self.assertFalse(result["ok"])
        self.assertIn("unknown sample", result["error"])


if __name__ == "__main__":
    unittest.main()
