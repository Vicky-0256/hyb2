"""Gzipped .hyb samples, the Import check's invalid rows and the bundled example data set."""
import gzip
import json
import sys
import tempfile
import unittest
from pathlib import Path

from desktop.runtime import samples
from desktop.runtime.bridge import handle
from desktop.runtime.project import new_project, validate_manifest

ROOT = Path(__file__).resolve().parents[3]
GOLDEN = ROOT / "pipeline" / "tests" / "golden" / "testData.hyb"
EXAMPLE = ROOT / "web" / "assets" / "examples" / "zikv-virion"
BAD_LINE = "bad_1\tACGU\t.\tZika_virusRNA\t1\t2\t0\t5\t1e-5\n"


class HybGzTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.project = self.dir / "p.hyb2proj"
        new_project(self.project, "Gzipped hyb")
        self.saved_path = list(sys.path)

    def tearDown(self):
        sys.path[:] = self.saved_path
        self.tmp.cleanup()

    def call(self, operation, **request):
        request.setdefault("path", str(self.project))
        response = handle({"operation": operation, **request}, repo_root=ROOT)
        self.assertTrue(response.get("ok"), response)
        return response

    def manifest(self):
        manifest = json.loads((self.project / "project.json").read_text(encoding="utf-8"))
        self.assertEqual(validate_manifest(manifest), [])
        return manifest

    def run_sample(self, sample_id, task_id):
        run_dir = self.project / "runs" / task_id
        run_dir.mkdir(parents=True)
        return self.call("sample_pipeline_run", path=None, project_path=str(self.project), sample_id=sample_id,
                         task_id=task_id, run_dir=str(run_dir))

    def gz_hyb(self, name, extra=""):
        path = self.dir / name
        lines = GOLDEN.read_text(encoding="utf-8").splitlines(keepends=True)
        with gzip.open(path, "wt", encoding="utf-8") as out:
            out.writelines(lines[:3] + [extra] + lines[3:200])
        return path

    def test_format_and_stem(self):
        self.assertEqual(samples.detect_read_format(Path("ZAFR_REP1_1in10.hyb.gz")), "hyb")
        self.assertEqual(samples.detect_read_format(Path("A.HYB.GZ")), "hyb")
        self.assertEqual(samples.read_stem(Path("ZAFR_REP1_1in10.hyb.gz")), "ZAFR_REP1_1in10")
        with self.assertRaisesRegex(ValueError, r"\.hyb\[\.gz\]"):
            samples.detect_read_format(Path("notes.txt.gz"))
        # A data folder proposes the .hyb.gz once, with its plain copy as a duplicate.
        folder = self.dir / "data"
        folder.mkdir()
        self.gz_hyb("data/S_REP1.hyb.gz")
        (folder / "S_REP1.hyb").write_text(GOLDEN.read_text(encoding="utf-8"))
        scan = samples.scan_data_folder(folder)
        self.assertEqual([(Path(s["path"]).name, s["format"], s["condition"], s["replicate"]) for s in scan["samples"]],
                         [("S_REP1.hyb.gz", "hyb", "S", 1)])
        self.assertEqual(len(scan["duplicates"]), 1)

    def test_gz_sample_is_decompressed_indexed_and_reports_invalid_rows(self):
        source = self.gz_hyb("given.hyb.gz", BAD_LINE)
        sample = self.call("add_sample", source=str(source), mode="copied", gene_id="Zika_virusRNA")["sample"]
        self.assertEqual(sample["reads"]["format"], "hyb")
        self.assertIsNone(sample["outputs"]["hyb"])  # the .gz is not an indexable output
        estimate = samples.estimate_sample(sample)
        self.assertEqual(estimate["uncompressed_bytes"], int(sample["reads"]["size_bytes"] * samples.GZ_EXPANSION))

        result = self.run_sample(sample["id"], "sample-gz")
        self.assertEqual(result["status"], "succeeded", result)
        stored = next(s for s in self.manifest()["samples"] if s["id"] == sample["id"])
        self.assertEqual([stored["stages"][k]["status"] for k in ("map", "hyb", "index")], ["skipped", "skipped", "succeeded"])
        self.assertEqual(stored["outputs"]["hyb"], "samples/%s/given.hyb" % sample["id"])
        plain = self.project / stored["outputs"]["hyb"]
        with gzip.open(source, "rb") as handle_:
            self.assertEqual(plain.read_bytes(), handle_.read())
        self.assertEqual(result["output"], str(plain.resolve()))
        self.assertTrue((self.project / stored["outputs"]["index"]).is_file())
        self.assertTrue((self.project / stored["outputs"]["contact"]).is_file())
        self.assertEqual(list((self.project / "samples" / sample["id"]).glob(".gunzip-*")), [])

        # Indexing again uses the decompressed copy.
        (self.project / "runs" / "index-again").mkdir()
        again = self.call("analysis_index_build", path=None, project_path=str(self.project), sample_id=sample["id"],
                          task_id="index-again", run_dir=str(self.project / "runs" / "index-again"))
        self.assertTrue(again["ok"], again)
        self.assertEqual(self.manifest()["samples"][0]["outputs"]["hyb"], stored["outputs"]["hyb"])

        overview = self.call("analysis_overview", path=None, project_path=str(self.project), sample_ids=[sample["id"]])["result"]
        self.assertEqual(overview["records"], 200)
        self.assertEqual(overview["invalid"], 1)
        self.assertEqual(overview["omitted_errors"], 0)
        error, = overview["errors"]
        self.assertEqual((error["line"], error["sample_id"], error["index"]), (4, sample["id"], 0))
        self.assertTrue(error["error"])
        self.assertEqual(error["content"], BAD_LINE.rstrip("\n"))

    def test_gunzip_file_limit(self):
        source = self.gz_hyb("big.hyb.gz")
        target = self.dir / "out" / "big.hyb"
        target.parent.mkdir()
        with self.assertRaisesRegex(ValueError, "decompresses to more than"):
            samples.gunzip_file(source, target, limit=100)
        self.assertEqual(list(target.parent.iterdir()), [])


@unittest.skipUnless((EXAMPLE / "ZAFR_REP1_1in10.hyb.gz").is_file(), "the example data set is not in this checkout")
class ExampleDataTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.project = Path(self.tmp.name) / "example.hyb2proj"
        new_project(self.project, "ZIKV virion example")

    def tearDown(self):
        self.tmp.cleanup()

    def call(self, operation, **request):
        return handle({"operation": operation, "path": str(self.project), **request}, repo_root=ROOT)

    def test_example_data_is_copied_into_an_empty_project(self):
        result = self.call("add_example_data")
        self.assertTrue(result["ok"], result)
        manifest = json.loads((self.project / "project.json").read_text(encoding="utf-8"))
        self.assertEqual(validate_manifest(manifest), [])
        self.assertEqual([(r["label"], r["sequences"][0]["name"], r["annotation"]["file_name"]) for r in manifest["references"]],
                         [("ZAFR", "ZIKV_African_KX377335-1_virusRNA", "NC_035889.gb"),
                          ("ZASI", "ZIKV_FrenchPolynesian_KJ776791-2_virusRNA", "NC_035889.gb")])
        rows = [(s["name"], s["condition"], s["replicate"], s["reads"]["format"], s["reads"]["mode"], s["gene_id"])
                for s in manifest["samples"]]
        self.assertEqual(rows, [
            ("ZAFR_REP1", "ZAFR", 1, "hyb", "copied", "ZIKV_African_KX377335-1_virusRNA"),
            ("ZAFR_REP2", "ZAFR", 2, "hyb", "copied", "ZIKV_African_KX377335-1_virusRNA"),
            ("ZILM_REP2", "ZASI", 2, "hyb", "copied", "ZIKV_FrenchPolynesian_KJ776791-2_virusRNA"),
            ("ZILM_REP3", "ZASI", 3, "hyb", "copied", "ZIKV_FrenchPolynesian_KJ776791-2_virusRNA")])
        for sample in manifest["samples"]:
            self.assertTrue((self.project / sample["reads"]["path"]).is_file())
            self.assertTrue(sample["reads"]["path"].startswith("inputs/"))
        # Nothing is linked from outside the project.
        self.assertEqual(self.call("list_untrusted_links")["files"], [])
        again = None
        with self.assertRaisesRegex(ValueError, "without references or samples"):
            again = self.call("add_example_data")
        self.assertIsNone(again)

    def test_example_sample_indexes_with_the_web_record_count(self):
        self.assertTrue(self.call("add_example_data")["ok"])
        sample = next(s for s in json.loads((self.project / "project.json").read_text(encoding="utf-8"))["samples"]
                      if s["name"] == "ZILM_REP3")
        run_dir = self.project / "runs" / "example-index"
        run_dir.mkdir(parents=True)
        result = handle({"operation": "sample_pipeline_run", "project_path": str(self.project), "sample_id": sample["id"],
                         "task_id": "example-index", "run_dir": str(run_dir)}, repo_root=ROOT)
        self.assertEqual(result["status"], "succeeded", result)
        overview = handle({"operation": "analysis_overview", "project_path": str(self.project), "sample_ids": [sample["id"]]},
                          repo_root=ROOT)["result"]
        self.assertEqual(overview["records"], 18466)  # web/example.js
        self.assertEqual(overview["invalid"], 0)

    def test_missing_example_files(self):
        with self.assertRaisesRegex(ValueError, "not bundled with this runtime"):
            handle({"operation": "add_example_data", "path": str(self.project)}, repo_root=Path(self.tmp.name))


if __name__ == "__main__":
    unittest.main()
