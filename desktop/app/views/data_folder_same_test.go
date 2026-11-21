package views

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"hyb2desktop/core"
)

// A file with the same content as another is not proposed, and the scan
// says which one it matches; an import that skips one for that reason does
// not call it "already in the project".
func TestDataFolderSameContentFiles(t *testing.T) {
	a, f := dataOpen(t, "list_samples.json")
	scan := fixture(t, "data_folder_scan.json")
	scan["duplicates"] = append(scan["duplicates"].([]any), map[string]any{
		"path": "/data/zika_files/ZAFR_copy.fastq.gz", "kept": "/data/zika_files/ZAFR_REP1.fastq.gz", "reason": "same content"})
	f.ops["scan_data_folder"] = scan
	f.ops["import_sample_rows"] = core.Obj{"ok": true, "samples": []any{1},
		"skipped": []any{
			map[string]any{"id": "ZAFR_REP1", "path": "/data/zika_files/ZAFR_REP2.fastq.gz", "reason": "already in project"},
			map[string]any{"id": "ZBRA_REP1", "path": "/data/zika_files/ZBRA_REP2.fastq.gz", "kept": "/data/zika_files/ZBRA_REP1.fastq.gz",
				"reason": "same content as another file of this import"}}}
	old := PickFolder
	PickFolder = func(string) string { return "/data/zika_files" }
	t.Cleanup(func() { PickFolder = old })
	tt := ui.NewTester(a.View, 1280, 820)
	dataClick(t, tt, a, "Import data folder…")
	waitFlush(a)
	tt.Frame()
	dataExpect(t, tt, "Not imported: 9 uncompressed copies of .gz files (the .gz is used); ZAFR_copy.fastq.gz (same content as ZAFR_REP1.fastq.gz); "+
		"5 .tab files; 30 Bowtie2 index files (HYB2 builds its own); 1 named FASTA but not FASTA content.")
	dataClick(t, tt, a, "Import 14 samples")
	tt.Frame()
	dataExpect(t, tt, "Imported 1 sample, 1 already in the project; not imported: ZBRA_REP2.fastq.gz (same content as ZBRA_REP1.fastq.gz). Next: Run all pending.")
}
