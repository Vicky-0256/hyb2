import json
import os
import random
import shutil
import subprocess
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
        for name in ("results.tsv", "significant.tsv", "interactions.tsv", "similarity.tsv", "summary.json"):
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

        # what the desktop scheduler does when the task ends
        run_dir = Path(result["run_dir"])
        (run_dir / "status.json").write_text(json.dumps({"task_id": "cmp1", "kind": "compare", "status": "succeeded"}))
        register_task_run(self.project, "cmp1")
        manifest = load_manifest(self.project)
        entry = manifest["comparisons"][0]
        self.assertEqual(entry["id"], "cmp1")
        self.assertEqual(entry["samples_one"], self.ids[:3])
        self.assertEqual(entry["paths"]["significant"], "comparisons/cmp1/significant.tsv")
        self.assertEqual(entry["paths"]["similarity"], "comparisons/cmp1/similarity.tsv")
        self.assertEqual(entry["summary"]["similarity_cells"], summary["similarity_cells"])
        self.assertEqual(validate_project_directory(self.project), [])

        listed = handle({"operation": "list_comparisons", "path": str(self.project)})
        self.assertEqual([item["id"] for item in listed["comparisons"]], ["cmp1"])
        self.assertTrue(listed["comparisons"][0]["available"])
        read = handle({"operation": "read_comparison", "path": str(self.project), "comparison_id": "cmp1", "limit": 5})
        self.assertEqual(len(read["cells"]["x"]), min(5, summary["significant"]))
        self.assertEqual(read["truncated"], summary["significant"] > 5)
        self.assertEqual(len(read["interactions"]), summary["interactions"])
        self.assertEqual(len(read["similarity"]["x"]), min(5, summary["similarity_cells"]))
        self.assertEqual(read["similarity"]["count"], sorted(read["similarity"]["count"], reverse=True))
        with self.assertRaisesRegex(ValueError, "does not exist"):
            handle({"operation": "read_comparison", "path": str(self.project), "comparison_id": "nope"})

    def _merged_contacts(self, out, ids, name):
        """One contact map of the samples' summed counts (their .hyb files concatenated)."""
        merged = {}
        for sample_id in ids:
            path = next((out / "contacts").glob(f"{sample_id}.*.contact.txt"))
            for line in path.read_text().splitlines():
                parts = line.split("\t")
                if len(parts) == 3 and parts[0].isdigit():
                    key = (parts[0], parts[1])
                    merged[key] = merged.get(key, 0) + int(parts[2])
        target = out / name
        target.write_text(f"# hybrids between {GENE} and {GENE}\nx\ty\tcount\n"
                          + "".join(f"{x}\t{y}\t{n}\n" for (x, y), n in merged.items()))
        return target

    @unittest.skipUnless(shutil.which("awk"), "awk is needed to run hyb2's plot_similarity_map awk")
    def test_similarity_matches_plot_similarity_map(self):
        for minimum in (1, 3):
            task = f"sim{minimum}"
            result = handle(self._request(task_id=task, parameters={"similarity_min": minimum}), repo_root=ROOT)
            self.assertTrue(result["ok"], result)
            out = self.project / "comparisons" / task
            maps = []
            for label, ids in (("A", self.ids[:3]), ("B", self.ids[3:])):
                merged = self._merged_contacts(out, ids, f"merged_{label}.contact.txt")
                filtered = out / f"merged_{label}_{minimum}.contact.txt"
                filtered.write_text("".join(line + "\n" for k, line in enumerate(merged.read_text().splitlines())
                                            if k < 2 or int(line.split("\t")[2]) >= minimum))
                maps.append(filtered)
            # the awk of bin/plot_similarity_map, unchanged
            script = (ROOT / "bin" / "plot_similarity_map").read_text()
            pipeline = next(line for line in script.splitlines() if line.startswith("awk 'n=x[$1,$2]"))
            merge = out / "A-B.merge.txt"  # plot_similarity_map: cat $a1 ... $b1 ... > $A1"-"$B1".merge.txt"
            merge.write_text("".join(path.read_text() for path in maps))
            pipeline = pipeline.split(" > ")[0].replace('$A1"-"$B1".merge.txt"', str(merge))
            text = subprocess.run(["bash", "-c", pipeline], capture_output=True, text=True, check=True).stdout
            expected = sorted(tuple(int(v) for v in line.split("\t")) for line in text.splitlines()
                              if line.split("\t")[0].isdigit())
            got = sorted(tuple(int(v) for v in line.split("\t")[:3])
                         for line in (out / "similarity.tsv").read_text().splitlines()[1:])
            self.assertGreater(len(expected), 0)
            self.assertEqual(got, expected, f"similarity_min {minimum}")

    def test_rejects_bad_requests(self):
        with self.assertRaisesRegex(ValueError, "both conditions"):
            handle(self._request(samples_two=[self.ids[0], self.ids[3]]), repo_root=ROOT)
        with self.assertRaisesRegex(ValueError, "at least three"):
            handle(self._request(samples_one=[self.ids[0]], samples_two=[self.ids[3]]), repo_root=ROOT)
        with self.assertRaisesRegex(ValueError, "similarity_min must be an integer"):
            handle(self._request(parameters={"similarity_min": 0}), repo_root=ROOT)
        with self.assertRaisesRegex(ValueError, "unknown compare parameters"):
            handle(self._request(parameters={"alpha": 0.1}), repo_root=ROOT)
        result = handle(self._request(samples_two=[self.ids[3], "missing"], task_id="cmp2"), repo_root=ROOT)
        self.assertFalse(result["ok"])
        self.assertIn("unknown sample", result["error"])


GENE_X, GENE_Y = "TEST_rnaX", "TEST_rnaY"
LENGTH_X, LENGTH_Y = 1000, 800
NODE = shutil.which("node")
REFERENCE_JS = Path(__file__).resolve().parent / "web_compare_reference.js"
TOP_MAPS_GOLDEN = ROOT / "desktop" / "app" / "views" / "testdata" / "compare_top_maps"


def _pair_rows(seed, enriched):
    """X x Y hybrids, about half written Y x X; ``enriched`` adds a block at
    X 300 x Y 500.  Rows the web parser rejects and X x X rows are mixed in."""
    rng = random.Random(seed)
    lines = ["# a comment line\n"]
    for k in range(1500 + enriched):
        if k < 1500:
            x0, y0 = rng.randint(1, LENGTH_X - 60), rng.randint(1, LENGTH_Y - 60)
        else:
            x0, y0 = 300 + rng.randint(0, 20), 500 + rng.randint(0, 20)
        x1, y1 = x0 + rng.randint(15, 40), y0 + rng.randint(15, 40)
        if rng.random() < 0.5:
            fields = (GENE_X, x0, x1, GENE_Y, y0, y1)
        else:
            fields = (GENE_Y, y0, y1, GENE_X, x0, x1)
        lines.append(f"{k}_1\tACGU\t.\t{fields[0]}\t1\t20\t{fields[1]}\t{fields[2]}\t1e-5\t{fields[3]}\t21\t40\t"
                     f"{fields[4]}\t{fields[5]}\t1e-5\t\t-10\tType_1\n")
    lines.append(f"x1_1\tACGU\t.\t{GENE_X}\t1\t20\t50\t70\t1e-5\t{GENE_X}\t21\t40\t400\t420\t1e-5\t\t-10\tType_1\n")
    lines.append(f"x2_1\tACGU\t.\t{GENE_X}\t1\t20\t90\t70\t1e-5\t{GENE_Y}\t21\t40\t400\t420\t1e-5\t\t-10\tType_1\n")
    lines.append(f"x3_1\tACGU\t.\t{GENE_X}\t1\t20\t50\t70\t1e-5\t{GENE_Y}\t21\t40\t400\t420\n")
    lines.append(f"x4_1\tACGU\t.\t{GENE_X}\t1\t20\t0\t70\t1e-5\t{GENE_Y}\t21\t40\t400\t420\t1e-5\t\t-10\tType_1\n")
    return "".join(lines)


def _golden_maps(maps):
    """A small top_maps.json for the Go test's golden files: the run's first
    region with 12 cells per replicate, plus a "down" copy of it in which the
    last replicate has no cell (the web's "No contacts in this window")."""
    up = maps["regions"][0]
    down = {**up, "direction": "down", "rank": 1, "log2FoldChange": -up["log2FoldChange"], "padj": 0.000123456}
    first = [cells[:12] for cells in maps["panels"][0]]
    return {**maps, "regions": [up, down], "panels": [first, first[:-1] + [[]]]}


def _cells(path):
    """{(x, y): count} of a contact.txt."""
    out = {}
    for line in Path(path).read_text().splitlines():
        parts = line.split("\t")
        if len(parts) == 3 and parts[0].isdigit():
            out[(int(parts[0]), int(parts[1]))] = int(parts[2])
    return out


class InterRnaCompareTests(unittest.TestCase):
    """RNA X != RNA Y, counted as the web's differential test counts it."""

    def setUp(self):
        from desktop.runtime.samples import add_reference, add_sample

        self.tmp = tempfile.TemporaryDirectory()
        base = Path(self.tmp.name)
        self.project = base / "p.hyb2proj"
        new_project(self.project, "Inter-RNA")
        ref = base / "ref.fa"
        ref.write_text(f">{GENE_X}\n" + "ACGU" * (LENGTH_X // 4) + f"\n>{GENE_Y}\n" + "ACGU" * (LENGTH_Y // 4) + "\n")
        reference = add_reference(self.project, ref, mode="linked")["reference"]
        self.ids, self.paths = [], []
        for name, seed, enriched in (("a1", 11, 260), ("a2", 12, 240), ("b1", 13, 0), ("b2", 14, 0)):
            path = base / f"{name}.hyb"
            path.write_text(_pair_rows(seed, enriched))
            sample = add_sample(self.project, path, name=name, reference_id=reference["id"], gene_id=GENE_X,
                                condition=name[0], mode="linked")
            self.ids.append(sample["id"])
            self.paths.append(path)

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, task="pair1", **parameters):
        request = {"operation": "compare_run", "project_path": str(self.project), "task_id": task,
                   "samples_one": self.ids[:2], "samples_two": self.ids[2:],
                   "parameters": {"label_one": "A", "label_two": "B", "gene2": GENE_Y, **parameters}}
        result = handle(request, repo_root=ROOT)
        self.assertTrue(result["ok"], result)
        return self.project / "comparisons" / task

    def test_pair_cells_by_hand(self):
        from desktop.runtime import compare

        hyb = Path(self.tmp.name) / "tiny.hyb"
        tail = "\t1e-5\t\t-10\tType_1\n"
        hyb.write_text(f"r1\tA\t.\t{GENE_X}\t1\t2\t5\t14\t1\t{GENE_Y}\t1\t2\t21\t29{tail}"   # x 0,10 x y 20
                       f"r2\tA\t.\t{GENE_Y}\t1\t2\t25\t25\t1\t{GENE_X}\t1\t2\t12\t12{tail}"  # swapped: x 10 x y 20
                       f"r3\tA\t.\t{GENE_X}\t1\t2\t5\t14\t1\t{GENE_X}\t1\t2\t21\t29{tail}")  # X x X: not counted
        keys, counts, records = compare.pair_cells(hyb, GENE_X, GENE_Y)
        self.assertEqual(records, 2)
        self.assertEqual({(k >> 32, k & 0xFFFFFFFF): int(c) for k, c in zip(keys.tolist(), counts.tolist())},
                         {(0, 20): 1, (10, 20): 2})

    def test_run_writes_pair_maps_and_exports(self):
        out = self._run()
        summary = json.loads((out / "summary.json").read_text())
        self.assertGreater(summary["significant_up"], 0)
        top = (out / "interactions.tsv").read_text().splitlines()[1].split("\t")
        self.assertEqual(top[0], "up")
        self.assertTrue(280 <= int(top[1]) <= 350 and 480 <= int(top[2]) <= 550, top)
        contacts = sorted((out / "contacts").glob("*.contact.txt"))
        self.assertEqual(len(contacts), 4)
        self.assertTrue(all(f"{GENE_X}__{GENE_Y}" in path.name for path in contacts))
        self.assertTrue(all(item["records"] == 1500 + extra for item, extra in zip(summary["inputs"], (260, 240, 0, 0))))
        counts = (out / "counts.table.txt").read_text().split("\n")
        self.assertEqual(counts[0], "feature_id\t" + "\t".join(self.ids))
        self.assertEqual(len(counts) - 1, summary["cells_total"])
        self.assertEqual((out / "names.table").read_text(), "\n".join(
            f"{i}\t{'condition_one' if k < 2 else 'condition_two'}" for k, i in enumerate(self.ids)))
        self.assertTrue((out / "sample_metadata.tsv").read_text().startswith(
            "sample_id\tcondition\tcondition_label\tfile_name\n" + f"{self.ids[0]}\tcondition_one\tA\ta1.hyb"))
        maps = json.loads((out / "top_maps.json").read_text())
        self.assertEqual([r["direction"] for r in maps["regions"]].count("up"), min(10, summary["interactions_up"]))
        self.assertEqual(maps["samples"][0]["rna_y"], GENE_Y)
        listed = handle({"operation": "list_comparisons", "path": str(self.project)})
        self.assertEqual(listed["comparisons"], [])  # not registered yet; registration copies gene2 with the rest
        self.assertEqual(json.loads((Path(self.project) / "runs" / "pair1" / "result.json").read_text())
                         ["comparison"]["parameters"]["gene2"], GENE_Y)

    def test_sample_without_pair_records_is_rejected(self):
        request = {"operation": "compare_run", "project_path": str(self.project), "task_id": "pair2",
                   "samples_one": self.ids[:2], "samples_two": self.ids[2:],
                   "parameters": {"gene2": "TEST_missing"}}
        result = handle(request, repo_root=ROOT)
        self.assertFalse(result["ok"])
        self.assertIn("size factors cannot be computed", result["error"])

    @unittest.skipUnless(NODE, "node runs the web app's own code")
    def test_matches_the_web(self):
        out = self._run(min_count=2, range=100, padj_cutoff=0.05)
        summary = json.loads((out / "summary.json").read_text())
        maps = json.loads((out / "top_maps.json").read_text())
        request = {"samples": [{"id": i, "label": n, "fileName": p.name, "condition": "A" if k < 2 else "B",
                                "path": str(p), "rnaX": GENE_X, "rnaY": GENE_Y}
                               for k, (i, n, p) in enumerate(zip(self.ids, ("a1", "a2", "b1", "b2"), self.paths))],
                   "labels": {"A": "A", "B": "B"}, "options": {"minCount": 2, "radius": 100, "padjCutoff": 0.05},
                   "probability": 0.95, "topMaps": _golden_maps(maps)}
        request_path = Path(self.tmp.name) / "request.json"
        request_path.write_text(json.dumps(request))
        web = json.loads(subprocess.run([NODE, str(REFERENCE_JS), str(request_path)], capture_output=True,
                                        text=True, check=True).stdout)
        # the same cells in every sample's map
        for item, cells, records in zip(summary["inputs"], web["cells"], web["records"]):
            self.assertEqual(_cells(item["contact"]), {(x, y): n for x, y, n in cells}, item["id"])
            self.assertEqual(item["records"], records)
        # the same export files, byte for byte
        self.assertEqual((out / "counts.table.txt").read_text(), web["counts"])
        self.assertEqual((out / "names.table").read_text(), web["names"])
        self.assertEqual((out / "sample_metadata.tsv").read_text(), web["metadata"])
        # the same test result
        s = web["summary"]
        for key, web_key in (("cells_total", "cellsTotal"), ("cells_tested", "cellsTested"), ("significant", "significant"),
                             ("significant_up", "significantUp"), ("interactions_up", "interactionsUp"),
                             ("interactions_down", "interactionsDown")):
            self.assertEqual(summary[key], s[web_key], key)
        rows =[line.split("\t") for line in (out / "interactions.tsv").read_text().splitlines()[1:]]
        self.assertEqual([(r[0], int(r[1]), int(r[2])) for r in rows],
                         [(r["direction"], r["x"], r["y"]) for r in web["interactions"]])
        for r, w in zip(rows, web["interactions"]):
            self.assertAlmostEqual(float(r[3]), w["log2FoldChange"], delta=1e-6 * abs(w["log2FoldChange"]))
            self.assertAlmostEqual(float(r[4]), w["padj"], delta=1e-5 * w["padj"])
        # Top 10 local maps: the same windows, cells and contrast cap
        self.assertEqual([(r["direction"], r["rank"], r["x1"], r["x2"], r["y1"], r["y2"]) for r in maps["regions"]],
                         [(r["direction"], r["rank"], r["x1"], r["x2"], r["y1"], r["y2"]) for r in web["maps"]["regions"]])
        for panel, web_panel in zip(maps["panels"], web["maps"]["panels"]):
            self.assertEqual(panel, [sorted(cells) for cells in web_panel])
        # the desktop writes the ZIP's files from top_maps.json; the Go test
        # compares them with these files, made by the web's mapFiles
        golden = {f["path"]: f["text"] for f in web["files"] if f["path"] != "parameters.json"}
        stored = {path.name: path.read_text() for path in TOP_MAPS_GOLDEN.glob("*") if path.name != "top_maps.json"}
        if os.environ.get("HYB2_WRITE_GOLDEN"):
            TOP_MAPS_GOLDEN.mkdir(parents=True, exist_ok=True)
            for old in TOP_MAPS_GOLDEN.glob("*"):
                old.unlink()
            for name, text in golden.items():
                (TOP_MAPS_GOLDEN / name).write_text(text)
            (TOP_MAPS_GOLDEN / "top_maps.json").write_text(json.dumps(request["topMaps"], indent=1))
            stored = golden
        self.assertEqual(json.loads((TOP_MAPS_GOLDEN / "top_maps.json").read_text()), request["topMaps"])
        self.assertEqual(stored, golden)


if __name__ == "__main__":
    unittest.main()
