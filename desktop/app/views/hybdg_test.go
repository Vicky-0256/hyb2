package views

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hyb2desktop/model"
)

// The result package says per sample where the .hyb dG column came from,
// and cites ViennaRNA, plus UNAFold for samples an earlier version analysed.
func TestProvenanceHybDGSource(t *testing.T) {
	duplex := &model.SampleEnergy{Engine: "rnaduplex", Program: "ViennaRNA RNAduplex", Version: "2.7.2",
		Parameters: "RNA, 37 °C (ViennaRNA defaults)", Scope: "each hybrid's two arms, intermolecular pairs only"}
	cofold := &model.SampleEnergy{Engine: "rnacofold", Program: "ViennaRNA RNAcofold", Version: "2.6.4", Parameters: "ViennaRNA defaults (37 °C)"}
	legacy := &model.SampleEnergy{Engine: legacyDGEngine, Program: "UNAFold", Version: "3.8.1",
		Executable: "/opt/conda/bin/prog", Parameters: "RNA, 37 °C, 1 M Na+ (defaults, as Hyb)", Scope: "each hybrid's two arms"}
	samples := []model.Sample{
		{ID: "s1", Name: "new", Reads: model.Reads{Format: "fastq.gz"}, Outputs: model.Outputs{Hyb: "samples/s1/a.hyb"}, Energy: duplex},
		{ID: "s2", Name: "imported", Reads: model.Reads{Format: "hyb"}, Energy: duplex},
		{ID: "s3", Name: "old", Reads: model.Reads{Format: "fastq"}, Outputs: model.Outputs{Hyb: "samples/s3/a.hyb"}},
		{ID: "s4", Name: "cofold", Reads: model.Reads{Format: "fastq"}, Outputs: model.Outputs{Hyb: "samples/s4/a.hyb"}, Energy: cofold},
		{ID: "s5", Name: "legacy", Reads: model.Reads{Format: "fastq"}, Outputs: model.Outputs{Hyb: "samples/s5/a.hyb"}, Energy: legacy},
	}
	want := map[string][2]string{
		"s1": {"rnaduplex", "ViennaRNA RNAduplex 2.7.2, RNA, 37 °C, each hybrid's two arms, intermolecular pairs only"},
		"s2": {"imported", "from the imported .hyb file"},
		"s3": {"not_computed", "not computed"},
		"s4": {"rnacofold", "ViennaRNA RNAcofold 2.6.4, 37 °C"},
		"s5": {legacyDGEngine, "UNAFold 3.8.1, RNA, 37 °C, 1 M Na+, each hybrid's two arms"}, // legacy-name: old sample
	}
	d := &reportData{Name: "dG"}
	for _, s := range samples {
		dg := hybDGSource(s)
		if w := want[s.ID]; dg.Source != w[0] || dg.Text != w[1] {
			t.Fatalf("%s: %+v, want %v", s.ID, dg, w)
		}
		d.Samples = append(d.Samples, reportSample{ID: s.ID, Label: s.Name, Format: s.Reads.Format, DG: dg})
		d.Settings = append(d.Settings, reportSettings{Sample: s.Name, Settings: "x", DG: dg.Source})
	}
	r := buildResultRecord(d, time.Now())
	raw, err := json.Marshal(r.provenance())
	if err != nil {
		t.Fatal(err)
	}
	var prov struct {
		Methods struct {
			HybEnergy struct {
				Samples []struct {
					Sample, Source, Text string
					Version              *string
				}
				Citation *string
			}
		}
	}
	if err := json.Unmarshal(raw, &prov); err != nil {
		t.Fatal(err)
	}
	he := prov.Methods.HybEnergy
	both := ViennaCitation + "; " + UNAFoldCitation // legacy-name: old sample's citation
	if len(he.Samples) != 5 || he.Citation == nil || *he.Citation != both {
		t.Fatalf("methods.hybEnergy: %s", raw)
	}
	for _, s := range he.Samples {
		if s.Text != want[s.Sample][1] || s.Source != want[s.Sample][0] {
			t.Fatalf("provenance %s: %+v", s.Sample, s)
		}
	}
	if strings.Contains(string(raw), "/opt/conda") {
		t.Fatal("provenance names a local program path")
	}
	md := r.reportMarkdown()
	for _, w := range []string{".hyb dG ViennaRNA RNAduplex 2.7.2", ".hyb dG from the imported .hyb file", ".hyb dG not computed", "Lorenz et al. (2011)", "Markham & M. Zuker (2008)"} {
		if !strings.Contains(md, w) {
			t.Fatalf("report.md lacks %q", w)
		}
	}
	if note := d.energyNote(); !strings.Contains(note, "Algorithms Mol Biol 6:26") || !strings.Contains(note, "Methods Mol Biol 453:3-31") {
		t.Fatalf("report note citations: %s", note)
	}
	// Only RNAduplex samples: ViennaRNA alone.
	d.Samples, d.Settings = d.Samples[:3], d.Settings[:3]
	if c := buildResultRecord(d, time.Now()).provenance()["methods"].(map[string]any)["hybEnergy"].(map[string]any)["citation"]; c != ViennaCitation {
		t.Fatalf("citation with RNAduplex samples only: %v", c)
	}
	if strings.Contains(d.energyNote(), "Markham") {
		t.Fatal("report note cites UNAFold without an old sample") // legacy-name: citation check
	}
	// No computed dG: no citation.
	d.Samples, d.Settings = d.Samples[1:3], d.Settings[1:3]
	if c := buildResultRecord(d, time.Now()).provenance()["methods"].(map[string]any)["hybEnergy"].(map[string]any)["citation"]; c != nil {
		t.Fatalf("citation without computed dG: %v", c)
	}
}

// samples[].energy decodes from the project manifest.
func TestSampleEnergyDecodes(t *testing.T) {
	var s model.Sample
	if err := json.Unmarshal([]byte(`{"id":"s1","energy":{"engine":"rnaduplex","version":"2.7.2","source":"computed"}}`), &s); err != nil {
		t.Fatal(err)
	}
	if s.Energy == nil || s.Energy.Engine != "rnaduplex" || s.Energy.Version != "2.7.2" {
		t.Fatalf("%+v", s.Energy)
	}
}
