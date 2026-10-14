import json
import shutil
import sys
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import _dumps_line, handle
from desktop.runtime.project import new_project

ROOT = Path(__file__).resolve().parents[3]
HYB = ROOT / "pipeline/tests/golden/testData.hyb"


class AnalysisBridgeTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        sys.path.insert(0, str(ROOT / "pipeline"))
        from hyb2analysis import index

        cls.tmp = tempfile.TemporaryDirectory()
        cls.project = Path(cls.tmp.name) / "p.hyb2proj"
        new_project(cls.project, "Analysis")
        for sample in ("S1", "S2"):
            (cls.project / "samples" / sample).mkdir(parents=True)
            index.build(HYB, cls.project / "samples" / sample / "testData.hyb2idx")
        cls.outside = Path(cls.tmp.name) / "linked.hyb2idx"
        shutil.copy(cls.project / "samples/S1/testData.hyb2idx", cls.outside)
        manifest = json.loads((cls.project / "project.json").read_text())
        manifest["samples"] = [
            {"id": "S1", "outputs": {"index": "samples/S1/testData.hyb2idx"}},
            {"id": "S2", "outputs": {"index": "samples/S2/testData.hyb2idx"}},
            {"id": "LINKED", "outputs": {"index": str(cls.outside)}},
            {"id": "PENDING", "outputs": {"index": None}},
            {"id": "ESCAPE", "outputs": {"index": "../linked.hyb2idx"}},
        ]
        (cls.project / "project.json").write_text(json.dumps(manifest))

    @classmethod
    def tearDownClass(cls):
        from hyb2analysis import query

        query.close_all()
        cls.tmp.cleanup()

    def call(self, **request):
        request.setdefault("project_path", str(self.project))
        response = handle(request, repo_root=ROOT)
        json.loads(_dumps_line(response))  # the sidecar must be able to serialise it
        return response

    def test_summary_merges_samples(self):
        one = self.call(operation="analysis_summary", sample_ids=["S1"])
        both = self.call(operation="analysis_summary", sample_ids=["S1", "S2"])
        self.assertTrue(one["ok"])
        self.assertEqual(one["result"]["merged"]["records"], 10411)
        self.assertEqual(both["result"]["merged"]["records"], 2 * 10411)
        self.assertEqual(both["sample_ids"], ["S1", "S2"])
        linked = self.call(operation="analysis_summary", sample_ids=["LINKED"])
        self.assertEqual(linked["indexes"], [str(self.outside.resolve())])

    def test_contact_bins_and_top(self):
        single = self.call(operation="analysis_contact_bins", sample_ids=["S1"], gene="18S_rRNA", bin_size=10)["result"]
        merged = self.call(operation="analysis_contact_bins", sample_ids=["S1", "S2"], gene="18S_rRNA", bin_size=10)["result"]
        self.assertEqual(merged["count"], [2 * c for c in single["count"]])
        self.assertEqual(set(single) >= {"bin_size", "length", "x", "y", "count"}, True)
        region = self.call(operation="analysis_contact_bins", sample_ids=["S1"], gene="Zika_virusRNA", bin_size=5,
                           region={"x": [100, 600], "y": [100, 900]}, types=["Type_1"])["result"]
        self.assertTrue(all(100 // 5 * 5 <= x <= 600 for x in region["x"]))
        top = self.call(operation="analysis_top", sample_ids=["S1", "S2"], gene="Zika_virusRNA", bin_size=50, n=3)["result"]
        self.assertEqual(len(top["rows"]), 3)
        self.assertEqual(top["rows"][0]["per_index"][0] * 2, top["rows"][0]["count"])

    def test_hybrids_and_explicit_index_paths(self):
        result = self.call(operation="analysis_hybrids", index_paths=[str(self.outside)],
                           filters={"gene1": "Zika_virusRNA", "gene2": "18S_rRNA"}, limit=3, order="reads")["result"]
        self.assertEqual(result["total"], 947)
        self.assertEqual(len(result["rows"]), 3)
        self.assertTrue(result["rows"][0]["name"])

    def test_errors(self):
        for request in (
            {"operation": "analysis_summary", "sample_ids": ["NOPE"]},
            {"operation": "analysis_summary", "sample_ids": ["PENDING"]},
            {"operation": "analysis_summary", "sample_ids": ["ESCAPE"]},
            {"operation": "analysis_summary", "sample_ids": "S1"},
            {"operation": "analysis_summary"},
            {"operation": "analysis_summary", "index_paths": [str(Path(self.tmp.name) / "missing")]},
            {"operation": "analysis_contact_bins", "sample_ids": ["S1"]},
            {"operation": "analysis_contact_bins", "sample_ids": ["S1"], "gene": "18S_rRNA", "bin_size": "10"},
            {"operation": "analysis_hybrids", "sample_ids": ["S1"], "filters": []},
            {"operation": "analysis_hybrids", "sample_ids": ["S1"], "limit": True},
        ):
            with self.assertRaises(ValueError, msg=request):
                self.call(**request)

    def test_missing_backend(self):
        with self.assertRaisesRegex(ValueError, "not bundled"):
            handle({"operation": "analysis_summary", "project_path": str(self.project), "sample_ids": ["S1"]},
                   repo_root=Path(self.tmp.name))


if __name__ == "__main__":
    unittest.main()
