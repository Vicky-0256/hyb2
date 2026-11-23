"""structure_run of a pasted or HYB record sequence ("source": "paste" | "record")."""
import json
import tempfile
import unittest
from pathlib import Path

from desktop.runtime.bridge import handle
from desktop.runtime.project import _structure_entry, new_project

from desktop.runtime.tests.test_structure import GENE, ROOT, _sequence

ROW = "r{k}\tACGU\t.\t{g1}\t1\t20\t{a0}\t{a1}\t1e-5\t{g2}\t21\t40\t{b0}\t{b1}\t1e-5\t\t-10\tType_1\n"
COORDINATE_CASES = json.loads(Path(__file__).with_name("coordinate_cases.json").read_text(encoding="utf-8"))
INVALID_COORDINATES = COORDINATE_CASES["invalid"] + [float(value) for value in COORDINATE_CASES["nonFinite"]]


class SequenceSourceTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.project = Path(self.tmp.name) / "p.hyb2proj"
        new_project(self.project, "Source")
        (self.project / "data").mkdir()
        self.one, self.two = _sequence(240, seed=3), _sequence(120, seed=8)
        (self.project / "data" / "ref.fa").write_text(f">{GENE} test\n{self.one}\n>OTHER_RNA\n{self.two}\n")
        rows = [ROW.format(k=k, g1=GENE, a0=45, a1=70, g2=GENE, b0=150, b1=175) for k in range(6)]
        rows += [ROW.format(k=10 + k, g1=GENE, a0=5, a1=30, g2="OTHER_RNA", b0=60, b1=85) for k in range(4)]
        (self.project / "data" / "a.hyb").write_text("".join(rows))
        # not in the reference (another seed), so folded from the sequence alone
        self.foreign = _sequence(90, seed=21)

    def tearDown(self):
        self.tmp.cleanup()

    def _run(self, **extra):
        request = {"operation": "structure_run", "project_path": str(self.project), "reference": "data/ref.fa",
                   "hyb_paths": ["data/a.hyb"], "gene_id": "pasted_sequence", "mode": "genome", "source": "paste"}
        request.update(extra)
        return handle(request, repo_root=ROOT)

    def _meta(self, task):
        return json.loads((self.project / "structures" / task / "meta.json").read_text())

    def _read(self, task, result):
        """Register the result as the task layer does, then read it back."""
        from desktop.runtime.structure import read_structure
        entry = _structure_entry(self.project.resolve(), task, result)
        manifest = json.loads((self.project / "project.json").read_text())
        manifest["structures"] = [entry]
        (self.project / "project.json").write_text(json.dumps(manifest))
        return entry, read_structure(self.project, task)

    def test_reference_windows_use_normalized_coordinates(self):
        reference = self.project / "data" / "ref.fa"
        source = {"source": "reference", "engine": "viennarna", "gene_id": "toy", "mode": "windows",
                  "windows": [{"start": 2, "end": 11}]}
        reference.write_bytes("\ufeff>toy\rA\u0085cgtacgtacgt\r".encode())
        result = self._run(task_id="window", **source)
        self.assertTrue(result["ok"], result)
        _, stored = self._read("window", result)
        record = stored["records"][0]
        self.assertEqual((record["window_start"], record["window_end"], record["sequence"]), (2, 11, "CGUACGUACG"))
        for raw in ("ACGUACGUACGUN", "ACGUANGUACGU", "ACGUACGUACGU\x1c"):
            reference.write_text(f">toy\n{raw}\n")
            with self.subTest(raw=raw), self.assertRaisesRegex(ValueError, "unsupported symbols"):
                self._run(task_id="invalid-window", **source)

    def test_raw_window_coordinates_are_checked_before_conversion(self):
        for engine in ("cplfold", "viennarna"):
            fields = dict(source="reference", engine=engine, gene_id=GENE, mode="windows",
                          parameters={"evidence": "none"} if engine == "cplfold" else {})
            for endpoint in ("start", "end"):
                for value in INVALID_COORDINATES:
                    window = {"start": 1, "end": 20}
                    window[endpoint] = value
                    with self.subTest(engine=engine, endpoint=endpoint, value=value):
                        before = set((self.project / "runs").iterdir())
                        with self.assertRaisesRegex(ValueError, "window 1 needs integer start and end"):
                            self._run(**fields, windows=[window])
                        self.assertEqual(set((self.project / "runs").iterdir()), before)

    def test_raw_window_coordinate_controls(self):
        for engine in ("cplfold", "viennarna"):
            fields = dict(source="reference", engine=engine, gene_id=GENE, mode="windows",
                          parameters={"evidence": "none"} if engine == "cplfold" else {})
            for case in COORDINATE_CASES["validWindows"]:
                start, end = case["input"]
                with self.subTest(engine=engine, start=start, end=end):
                    result = self._run(**fields, windows=[{"start": start, "end": end}])
                    left, right = case["expected"]
                    meta = self._assert_sequence(result, self.one[left - 1:right])
                    self.assertEqual(meta["mode"], "windows")
                    self.assertEqual(meta["windows"], [{"start": left, "end": right}])
                    self.assertIs(type(meta["windows"][0]["start"]), int)
                    self.assertIs(type(meta["windows"][0]["end"]), int)
            for start, end in [(20, 1), ("20", "1")] + [case["input"] for case in COORDINATE_CASES["safeMaximumWindows"]]:
                with self.subTest(engine=engine, start=start, end=end), self.assertRaisesRegex(ValueError, "outside 1-240"):
                    self._run(**fields, windows=[{"start": start, "end": end}])

    def test_raw_two_region_coordinates_are_checked_before_conversion(self):
        for engine, mode in (("cplcofold", "cofold"), ("comrades", "paired")):
            fields = dict(source="reference", engine=engine, mode=mode, gene_id=GENE,
                          parameters={"evidence": "none"} if engine == "cplcofold" else {"random_folds": 0})
            for index in (0, 1):
                for endpoint in ("start", "end"):
                    for value in INVALID_COORDINATES:
                        regions = [{"gene_id": GENE, "start": 1, "end": 40},
                                   {"gene_id": "OTHER_RNA", "start": 50, "end": 90}]
                        regions[index][endpoint] = value
                        with self.subTest(engine=engine, region=index + 1, endpoint=endpoint, value=value):
                            before = set((self.project / "runs").iterdir())
                            with self.assertRaisesRegex(ValueError, f"region {index + 1} needs gene_id, start and end"):
                                self._run(**fields, regions=regions)
                            self.assertEqual(set((self.project / "runs").iterdir()), before)

    def test_raw_two_region_coordinate_controls(self):
        expected = [{"gene_id": GENE, "start": 2, "end": 35},
                    {"gene_id": "OTHER_RNA", "start": 55, "end": 91}]
        for engine, mode in (("cplcofold", "cofold"), ("comrades", "paired")):
            fields = dict(source="reference", engine=engine, mode=mode, gene_id=GENE,
                          parameters={} if engine == "cplcofold" else {"random_folds": 0})
            for values in ((2, 35, 55, 91), ("2", " 35 ", "055", "\t91\n"), (2.0, 35.0, 55.0, 91.0)):
                regions = [dict(region, start=values[2 * i], end=values[2 * i + 1]) for i, region in enumerate(expected)]
                with self.subTest(engine=engine, values=values):
                    result = self._run(**fields, regions=regions)
                    meta = self._assert_sequence(result, self.one[1:35] + "&" + self.two[54:91])
                    self.assertEqual([{key: region[key] for key in ("gene_id", "start", "end")} for region in meta["regions"]], expected)
                    self.assertEqual([(r["prepared_start"], r["prepared_end"]) for r in meta["regions"]], [(1, 34), (35, 71)])
                    self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 4)
            for index in (0, 1):
                regions = [dict(region) for region in expected]
                regions[index]["start"], regions[index]["end"] = regions[index]["end"], regions[index]["start"]
                with self.subTest(engine=engine, backwards=index), self.assertRaisesRegex(ValueError, f"region {index + 1}.*outside"):
                    self._run(**fields, regions=regions)

    def test_pasted_sequence_not_in_reference_is_sequence_only(self):
        # read as the web reads a paste: FASTA header, whitespace, lower case, T
        pasted = "\ufeff>mine & sample 2\r1 " + self.foreign[:45].lower().replace("u", "t") + "\r\n46  " + self.foreign[45:] + "\n"
        result = self._run(task_id="p1", sequence=pasted)
        self.assertTrue(result["ok"], result)
        meta = self._meta("p1")
        self.assertEqual(meta["sequence_source"]["kind"], "paste")
        self.assertNotIn("mapped_to", meta["sequence_source"])
        self.assertEqual(meta["parameters"]["evidence"], "none")
        self.assertEqual(meta["parameters"]["sequence_source"], "paste")
        self.assertIn("folded sequence", meta["coordinates"])
        dbn = (self.project / "structures" / "p1" / "structure.dbn").read_text().splitlines()
        self.assertTrue(dbn[0].startswith(">pasted_sequence:1-90 "), dbn[0])
        self.assertEqual(dbn[1], self.foreign)
        ct = (self.project / "structures" / "p1" / "structure.ct").read_text().splitlines()
        self.assertEqual(len(ct), 91)
        # result.json and the project.json entry name the source
        stored = json.loads((Path(result["run_dir"]) / "result.json").read_text())
        self.assertEqual(stored["structure"]["parameters"]["sequence_source"], "paste")
        entry, read = self._read("p1", result)
        self.assertEqual((entry["gene_id"], entry["parameters"]["sequence_label"]), ("pasted_sequence", "pasted_sequence"))
        record = read["records"][0]
        self.assertEqual((record["window_start"], record["window_end"], record["sequence"]), (1, 90, self.foreign))

    def test_pseudoknot_flag_on_native_fold_requests(self):
        seq = "GGCGCGGCACCGUCCGCGGAACAAACGG"
        for cofold in (False, True):
            strands = {"sequence": seq[:14], "sequence_two": seq[14:], "engine": "cplcofold", "mode": "cofold"} if cofold else {"sequence": seq}
            predict = {"operation": "cplfold_predict", "sequence": seq, **({"cutpoint": 14} if cofold else {})}
            for flags, expected in (({}, False), ({"allowPseudoknot": True}, True), ({"allow_pseudoknot": False}, False)):
                with self.subTest(cofold=cofold, flags=flags):
                    task = self._run(**strands, parameters={"evidence": "none", **flags})
                    self.assertIs(task["structure"]["parameters"]["allow_pseudoknot"], expected)
                    self.assertIs(task["performance"]["pseudoknot"], expected)
                    direct = handle({**predict, "parameters": flags}, repo_root=ROOT)
                    self.assertIs(direct["parameters"]["allowPseudoknot"], expected)
            for value in (None, 0, 1, "true", "false"):
                flags = {"allowPseudoknot": value}
                with self.subTest(cofold=cofold, value=value):
                    with self.assertRaisesRegex(ValueError, "must be a boolean"):
                        handle({**predict, "parameters": flags}, repo_root=ROOT)
                    with self.assertRaisesRegex(ValueError, "must be a boolean"):
                        self._run(**strands, parameters={"evidence": "none", **flags})
        with self.assertRaisesRegex(ValueError, "conflicting pseudoknot"):
            self._run(sequence=seq, parameters={"evidence": "none", "allow_pseudoknot": True, "allowPseudoknot": False})

    def test_native_structure_beam_limits(self):
        from unittest.mock import patch
        from desktop.runtime.structure import load_cplfold_web

        shared = load_cplfold_web(ROOT)
        for cofold in (False, True):
            strands = {"sequence": self.foreign[:12]}
            if cofold:
                strands.update(engine="cplcofold", mode="cofold", sequence_two=self.foreign[12:24])
            for beam in (200, 201):
                settings = {"evidence": "none", "beam": beam}
                with self.subTest(cofold=cofold, beam=beam):
                    if cofold and beam == 201:
                        with self.assertRaisesRegex(ValueError, "out of bounds: beam"):
                            self._run(**strands, parameters=settings)
                    else:
                        with patch.object(shared, "engine_parameters", wraps=shared.engine_parameters) as policy:
                            result = self._run(**strands, parameters=settings)
                        self.assertEqual(result["structure"]["parameters"]["beam"], beam)
                        if cofold:  # both catalogs currently cap cofold at 200
                            policy.assert_called_once_with("desktop")

    def test_native_cli_task_preserves_pseudoknot_mode_in_saved_report(self):
        request = {"operation": "cplfold_run", "project_path": str(self.project), "sequence": "GGCGCGGCACCGUCCGCGGAACAAACGG"}
        for flags, enabled in (({}, False), ({"no_pseudoknot": True}, False), ({"no_pseudoknot": False}, True)):
            with self.subTest(flags=flags):
                result = handle({**request, "parameters": flags}, repo_root=ROOT)
                self.assertTrue(result["ok"], result)
                report = Path(result["output"]).read_text()
                if enabled:
                    self.assertRegex(report, r"Pseudoknot Structures: [1-9]\d*")
                else:
                    self.assertIn("Pseudoknot Structures: disabled", report)
        for flags, error in (({"no_pseudoknot": "false"}, "must be a boolean"),
                             ({"no_pseudoknot": True, "allowPseudoknot": True}, "conflicting pseudoknot")):
            with self.subTest(flags=flags), self.assertRaisesRegex(ValueError, error):
                handle({**request, "parameters": flags}, repo_root=ROOT)

    def test_pasted_region_of_the_reference_uses_its_chimeras(self):
        result = self._run(task_id="p2", sequence=self.one[39:200])
        self.assertTrue(result["ok"], result)
        meta = self._meta("p2")
        self.assertEqual(meta["sequence_source"]["mapped_to"], [f"{GENE}:40-200"])
        self.assertEqual((meta["mode"], meta["windows"], meta["gene_id"]), ("windows", [{"start": 40, "end": 200}], GENE))
        self.assertEqual(meta["parameters"]["evidence"], "hyb")
        self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 6)
        # asked for sequence only, it is not searched for and stays sequence only
        alone = self._run(task_id="p3", sequence=self.one[39:200], parameters={"evidence": "none"})
        self.assertTrue(alone["ok"], alone)
        self.assertEqual(self._meta("p3")["gene_id"], "pasted_sequence")

    def test_record_sequence_with_viennarna(self):
        result = self._run(task_id="r1", source="record", engine="viennarna", gene_id="record 17_3",
                           sequence=self.foreign, parameters={"min_loop": 3})
        self.assertTrue(result["ok"], result)
        meta = self._meta("r1")
        self.assertEqual((meta["engine"], meta["gene_id"]), ("ViennaRNA", "record_17_3"))
        self.assertEqual(meta["parameters"]["sequence_source"], "record")

    def _assert_sequence(self, result, sequence):
        self.assertTrue(result["ok"], result)
        lines = Path(result["outputs"]["dbn"]).read_text().splitlines()
        self.assertEqual(lines[1], sequence)
        self.assertEqual(len(lines[2]), len(sequence))
        return self._meta(result["task_id"])

    def test_source_length_policy_boundaries(self):
        reference = self.project / "data" / "ref.fa"
        for engine in ("cplfold", "viennarna"):
            params = {"evidence": "none"} if engine == "cplfold" else {}
            for length in (4, 5, 7):
                with self.subTest(engine=engine, source="paste", length=length):
                    fields = dict(engine=engine, parameters=params, sequence=">mine\r1 " + "g" * length + "\u0085")
                    if length < 5:
                        with self.assertRaisesRegex(ValueError, "shorter than 5 nt"):
                            self._run(**fields)
                    else:
                        self._assert_sequence(self._run(**fields), "G" * length)
            for source in ("record", "reference"):
                for sequence in ("\u0085", " t\u0085"):
                    with self.subTest(engine=engine, source=source, sequence=sequence):
                        fields = dict(engine=engine, source=source, gene_id="tiny", parameters=params)
                        if source == "record":
                            fields["sequence"] = sequence
                        else:
                            reference.write_text(">tiny\n" + sequence + "\n")
                        if not sequence.strip():
                            with self.assertRaisesRegex(ValueError, "shorter than 1 nt"):
                                self._run(**fields)
                        else:
                            self._assert_sequence(self._run(**fields), "U")
            reference.write_text(">tiny\nGGGG\n")
            fields = dict(engine=engine, source="reference", gene_id="tiny", parameters=params)
            self._assert_sequence(self._run(**fields), "GGGG")
            # A window spanning every base is still an explicit window, not genome mode.
            with self.assertRaisesRegex(ValueError, "shorter than 10 nt"):
                self._run(**fields, mode="windows", windows=[{"start": 1, "end": 4}])
            reference.write_text(">tiny\nA\u0085cgtacgtacgt\n")
            for start, end in ((2, 10), (2, 11), (11, 2)):
                with self.subTest(engine=engine, window=(start, end)):
                    fields = dict(engine=engine, source="reference", gene_id="tiny", mode="windows",
                                  windows=[{"start": start, "end": end}], parameters=params)
                    if start > end:
                        with self.assertRaisesRegex(ValueError, "outside 1-12"):
                            self._run(**fields)
                    elif end - start + 1 < 10:
                        with self.assertRaisesRegex(ValueError, "shorter than 10 nt"):
                            self._run(**fields)
                    else:
                        self._assert_sequence(self._run(**fields), "CGUACGUACG")

    def test_fragment_length_policy_boundaries(self):
        reference = self.project / "data" / "ref.fa"
        reference.write_text(">A\nAAg\u0085gggUU\n>B\nAAccccUU\n")
        for length in (1, 4):
            (self.project / "data" / "a.hyb").write_text(ROW.format(k=1, g1="A", a0=3, a1=length + 2,
                                                                    g2="B", b0=3, b1=length + 2))
            for joined in (False, True):
                fields = dict(engine="cplcofold", mode="cofold", parameters={"evidence": "none"},
                              sequence=">A\n1 " + "g" * length, sequence_two=" c" * length)
                if joined:
                    fields.update(sequence=fields["sequence"] + " & 2 " + fields["sequence_two"], sequence_two="")
                with self.subTest(source="paste", length=length, joined=joined):
                    self._assert_sequence(self._run(**fields), "G" * length + "&" + "C" * length)
            for engine, mode in (("cplcofold", "cofold"), ("comrades", "paired")):
                with self.subTest(engine=engine, source="reference", length=length):
                    regions = [{"gene_id": gene, "start": 3, "end": length + 2} for gene in ("A", "B")]
                    fields = dict(source="reference", gene_id="A", engine=engine, mode=mode, regions=regions,
                                  parameters={} if engine == "cplcofold" else {"random_folds": 0})
                    result = self._run(**fields)
                    if engine == "comrades" and length == 1:
                        # The layout is valid, but ViennaRNA 2.7.2 returns an empty
                        # G&C dimer structure. Preserve this engine failure, not a
                        # fabricated success or a new task-length restriction.
                        self.assertEqual(result["status"], "failed")
                        self.assertEqual(result["error"], "ViennaRNA RNAcofold returned an invalid two-strand structure.")
                    else:
                        meta = self._assert_sequence(result, "G" * length + "&" + "C" * length)
                        self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 1)
                    for index in (0, 1):
                        backwards = [dict(region) for region in regions]
                        backwards[index].update(start=6, end=3)
                        with self.assertRaisesRegex(ValueError, f"region {index + 1}.*outside"):
                            self._run(**dict(fields, regions=backwards))
        meta = self._assert_sequence(self._run(engine="cplcofold", mode="cofold", sequence="gggg", sequence_two="cccc"), "GGGG&CCCC")
        self.assertEqual(meta["sequence_source"]["mapped_to"], ["A:3-6", "B:3-6"])
        self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 1)
        for first, second in (("", "C"), ("G", ""), ("G&", ""), ("&C", "")):
            with self.subTest(first=first, second=second), self.assertRaisesRegex(ValueError, "shorter than 1 nt"):
                self._run(engine="cplcofold", mode="cofold", sequence=first, sequence_two=second,
                          parameters={"evidence": "none"})

    def test_short_mapped_sources_keep_original_policy_and_evidence(self):
        (self.project / "data" / "ref.fa").write_text(">tiny\nAAGGGGCCCUU\n")
        (self.project / "data" / "a.hyb").write_text(ROW.format(k=1, g1="tiny", a0=3, a1=5, g2="tiny", b0=7, b1=9))
        for source in ("paste", "record"):
            for engine in ("cplfold", "comrades"):
                with self.subTest(source=source, engine=engine):
                    meta = self._assert_sequence(self._run(source=source, engine=engine, sequence="ggggccc",
                                                          parameters={} if engine == "cplfold" else {"random_folds": 0}), "GGGGCCC")
                    self.assertEqual(meta["sequence_source"]["mapped_to"], ["tiny:3-9"])
                    self.assertEqual(meta["sequence_source"]["kind"], source)
                    self.assertEqual(meta["windows"], [{"start": 3, "end": 9}])
                    self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 1)
                    if engine == "cplfold":
                        self.assertEqual(meta["parameters"]["evidence"], "hyb")
        (self.project / "data" / "ref.fa").write_text(">tiny\nAAGUU\n")
        (self.project / "data" / "a.hyb").write_text(ROW.format(k=1, g1="tiny", a0=3, a1=3, g2="tiny", b0=3, b1=3))
        meta = self._assert_sequence(self._run(source="record", sequence="g"), "G")
        self.assertEqual(meta["sequence_source"]["mapped_to"], ["tiny:3-3"])
        self.assertEqual(meta["records"][0]["evidence"]["inputRecords"], 1)

    def test_length_definitions_drive_native_tasks(self):
        from unittest.mock import patch
        from pipeline.hyb2analysis import reference

        (self.project / "data" / "ref.fa").write_text(">tiny\nGGGGGGGGGGGG\n>other\nCCCCCCCCCCCC\n")
        sequence_only = {"parameters": {"evidence": "none"}}
        with patch.object(reference, "MIN_PASTED_NT", 6), patch.object(reference, "MIN_WINDOW_NT", 11):
            with self.assertRaisesRegex(ValueError, "shorter than 6 nt"):
                self._run(sequence="G" * 5, **sequence_only)
            self._assert_sequence(self._run(sequence="G" * 6, **sequence_only), "G" * 6)
            with self.assertRaisesRegex(ValueError, "shorter than 11 nt"):
                self._run(source="reference", gene_id="tiny", mode="windows", windows=[{"start": 1, "end": 10}], **sequence_only)
            self._assert_sequence(self._run(source="reference", gene_id="tiny", mode="windows",
                                            windows=[{"start": 1, "end": 11}], **sequence_only), "G" * 11)
            # Neither a short whole record nor two fragments inherit either cutoff.
            self._assert_sequence(self._run(source="record", sequence="G", **sequence_only), "G")
            self._assert_sequence(self._run(sequence="G", sequence_two="C", engine="cplcofold", mode="cofold", **sequence_only), "G&C")
        with patch.object(reference, "MIN_SEQUENCE_NT", 11):
            # minSequenceNt is not a global floor on explicit windows or a single paste.
            self._assert_sequence(self._run(sequence="G" * 5, **sequence_only), "G" * 5)
            self._assert_sequence(self._run(source="reference", gene_id="tiny", mode="windows",
                                            windows=[{"start": 1, "end": 10}], **sequence_only), "G" * 10)
            for source in ("record", "reference"):
                for length in (10, 11):
                    fields = dict(source=source, gene_id="tiny", **sequence_only)
                    if source == "record":
                        fields["sequence"] = "G" * length
                    else:
                        (self.project / "data" / "ref.fa").write_text(">tiny\n" + "G" * length + "\n>other\n" + "C" * 12 + "\n")
                    with self.subTest(source=source, length=length):
                        if length < 11:
                            with self.assertRaisesRegex(ValueError, "shorter than 11 nt"):
                                self._run(**fields)
                        else:
                            self._assert_sequence(self._run(**fields), "G" * length)
            for engine, mode in (("cplcofold", "cofold"), ("comrades", "paired")):
                with self.subTest(engine=engine), self.assertRaisesRegex(ValueError, "region 1 is shorter than 11 nt"):
                    self._run(source="reference", gene_id="tiny", engine=engine, mode=mode,
                              regions=[{"gene_id": gene, "start": 1, "end": 10} for gene in ("tiny", "other")])
            with self.assertRaisesRegex(ValueError, "RNA B is shorter than 11 nt"):
                self._run(engine="cplcofold", mode="cofold", sequence="G" * 11, sequence_two="C" * 10, **sequence_only)
            self._assert_sequence(self._run(engine="cplcofold", mode="cofold", sequence="G" * 11, sequence_two="C" * 11,
                                            **sequence_only), "G" * 11 + "&" + "C" * 11)
            # The prepared-engine bridge is deliberately not a task/form input policy.
            prepared = handle({"operation": "cplfold_predict", "sequence": "G", "parameters": {"evidenceMode": "none"}}, repo_root=ROOT)
            self.assertTrue(prepared["ok"], prepared)
            self.assertEqual(prepared["sequence"], "G")

    def test_private_fields_cannot_skip_source_policy(self):
        fields = {"_source": {"kind": "record"}, "_strands": {GENE: "G"}, "_located_ids": {GENE: GENE},
                  "_minimum_length": 1, "parameters": {"evidence": "none"}}
        with self.assertRaisesRegex(ValueError, "shorter than 5 nt"):
            self._run(sequence="GGGG", **fields)
        with self.assertRaisesRegex(ValueError, "shorter than 10 nt"):
            self._run(source="reference", gene_id=GENE, mode="windows", windows=[{"start": 1, "end": 7}], **fields)

    def test_comrades_needs_a_reference_region(self):
        with self.assertRaisesRegex(ValueError, "ViennaRNA MFE or sequence-only CPLfold"):
            self._run(task_id="c1", engine="comrades", sequence=self.foreign)

    def test_two_pasted_strands_with_cplcofold(self):
        cofold = {"engine": "cplcofold", "mode": "cofold", "gene_id": "pasted"}
        result = self._run(task_id="co1", sequence=self.foreign[:50], sequence_two=self.foreign[50:], **cofold)
        self.assertTrue(result["ok"], result)
        meta = self._meta("co1")
        self.assertEqual([r["gene_id"] for r in meta["regions"]], ["pasted_A", "pasted_B"])
        self.assertTrue(meta["evidence_inputs"]["rows"].startswith("none"))
        dbn = (self.project / "structures" / "co1" / "structure.dbn").read_text().splitlines()
        self.assertEqual(dbn[1], self.foreign[:50] + "&" + self.foreign[50:])
        _, read = self._read("co1", result)
        self.assertEqual(read["records"][0]["cutpoint"], 50)
        # one A&B sequence, the RNA B box empty
        same = self._run(task_id="co2", sequence=">A & B\r1 " + self.foreign[:50] + " & 51 " + self.foreign[50:], **cofold)
        self.assertTrue(same["ok"], same)
        self.assertEqual((self.project / "structures" / "co2" / "structure.dbn").read_text().splitlines()[1], dbn[1])
        # both strands are regions of the reference: folded as those regions, with their chimeras
        mapped = self._run(task_id="co3", sequence=self.one[:80], sequence_two=self.two[40:100], **cofold)
        self.assertTrue(mapped["ok"], mapped)
        meta = self._meta("co3")
        self.assertEqual(meta["sequence_source"]["mapped_to"], [f"{GENE}:1-80", "OTHER_RNA:41-100"])
        self.assertEqual(meta["records"][0]["evidence_rows"]["between"], 4)

    def test_cplcofold_duplex_runs_within_the_task_cpus(self):
        # RNAduplex read support forks one process per worker: CPLcofold must not start
        # more than the task's CPU claim (HYB2_TASK_CPUS)
        import os
        from unittest import mock
        from desktop.runtime import structure
        module = structure.load_cplfold_web(ROOT)
        cofold = {"engine": "cplcofold", "mode": "cofold", "gene_id": "pasted"}
        with mock.patch.object(module, "fold", wraps=module.fold) as fold, \
                mock.patch.dict(os.environ, {"HYB2_TASK_CPUS": "1"}):
            result = self._run(task_id="cpu1", sequence=self.foreign[:50], sequence_two=self.foreign[50:], **cofold)
        self.assertTrue(result["ok"], result)
        self.assertEqual([call.args[0].get("duplexWorkers") for call in fold.call_args_list], [1])

    def test_fold_engines_check_the_narrowed_alphabet_before_computing(self):
        from unittest.mock import patch
        from desktop.runtime import comrades, iris, mfold, vienna
        from hyb2analysis.fold_text import FOLD_TEXT

        with patch.dict(FOLD_TEXT, alphabet="ACG"):
            seq = "GGGGtAAACCCC"
            for fold, args in ((iris.run, (seq, [], {})), (comrades.fold, (seq, [], comrades.parameters({}))),
                               (vienna.fold, (seq, vienna.parameters({}))), (mfold.fold, (seq, {}))):
                with self.subTest(engine=fold.__module__), self.assertRaisesRegex(ValueError, "unsupported symbols: U"):
                    fold(*args)
            valid = "gggg aaaacccc"
            self.assertEqual(vienna.fold(valid, vienna.parameters({}))["structure"], "((((....))))")
            with self.assertRaisesRegex(ValueError, "requires HYB evidence"):
                iris.run(valid, [], {})

    def test_rejects(self):
        for extra in ({"sequence": self.foreign[:30] + "N"}, {"sequence": "ACGU"}, {"sequence": 5},
                      {"sequence": ">first\n" + self.foreign + "\n>second\nACGU"},
                      {"sequence": "1 " + self.foreign, "source": "record"},
                      {"sequence": self.foreign, "mode": "windows", "windows": [{"start": 1, "end": 40}]},
                      {"sequence": self.foreign, "sequence_two": self.foreign},
                      {"sequence": self.foreign, "source": "reference"}, {"sequence": self.foreign, "source": "file"},
                      {"sequence": self.foreign, "source": "record", "engine": "cplcofold", "mode": "cofold",
                       "sequence_two": self.foreign},
                      {"sequence": "A&C&G", "engine": "cplcofold", "mode": "cofold"}):
            with self.assertRaises(ValueError, msg=extra):
                self._run(task_id="bad", **extra)


if __name__ == "__main__":
    unittest.main()
