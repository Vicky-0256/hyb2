package model

import (
	"slices"
	"testing"
)

func TestGeneReferencePreservesUnqualifiedLookup(t *testing.T) {
	for _, secondNT := range []int{20, 40} {
		p := &Project{References: []Reference{
			{ID: "R1", RNASequences: []Sequence{{Name: "shared", Length: 20}}},
			{ID: "R2", RNASequences: []Sequence{{Name: "shared", Length: secondNT}}},
		}}
		for range 2 {
			first := &p.References[0]
			want := first.RNASequences[0].Length
			if ref, length := p.GeneReference("shared"); ref != first || length != want || p.GeneLength("shared") != want {
				t.Errorf("unqualified lookup changed its legacy first-match contract: got %v (%d nt), want %s (%d nt)", ref, length, first.ID, want)
			}
			for id, want := range map[string]int{"R1": 20, "R2": secondNT} {
				if ref, length := p.GeneReference("shared", id); ref == nil || ref.ID != id || length != want {
					t.Errorf("qualified %s/shared: got %v, %d nt; want %d nt", id, ref, length, want)
				}
			}
			if ref, length := p.GeneReference("shared", "missing"); ref != nil || length != 0 {
				t.Errorf("a missing explicit reference fell back to %v, %d nt", ref, length)
			}
			slices.Reverse(p.References)
		}
	}
}

func TestGeneReferenceUsesSelectableRNAs(t *testing.T) {
	p := &Project{References: []Reference{
		{ID: "mapped", Sequences: []Sequence{{Name: "physical", Length: 40}}, RNASequences: []Sequence{{Name: "alias", Length: 40}}},
		{ID: "legacy", Sequences: []Sequence{{Name: "legacy-rna", Length: 20}}},
		{ID: "unmatched", Sequences: []Sequence{{Name: "unmatched-rna", Length: 30}}, RNASequences: []Sequence{}},
	}}
	for _, tc := range []struct {
		name, ref string
		length    int
	}{{"alias", "mapped", 40}, {"legacy-rna", "legacy", 20}, {"physical", "", 0}, {"unmatched-rna", "", 0}, {"missing", "", 0}} {
		ref, length := p.GeneReference(tc.name)
		id := ""
		if ref != nil {
			id = ref.ID
		}
		if id != tc.ref || length != tc.length {
			t.Errorf("%s: got %s/%d, want %s/%d", tc.name, id, length, tc.ref, tc.length)
		}
	}
	if ref, length := p.GeneReference("alias", "legacy"); ref != nil || length != 0 {
		t.Errorf("an absent alias on the explicit reference fell back to %v, %d nt", ref, length)
	}
}

func TestTaskRegistrationDecoding(t *testing.T) {
	for _, tc := range []struct {
		name          string
		fields        map[string]any
		status, error string
	}{
		{name: "missing"},
		{name: "null", fields: map[string]any{"registration_status": nil, "registration_error": nil}},
		{name: "empty", fields: map[string]any{"registration_status": "", "registration_error": ""}},
		{name: "pending", fields: map[string]any{"registration_status": "pending", "registration_error": nil}, status: "pending"},
		{name: "failed", fields: map[string]any{"registration_status": "failed", "registration_error": "project manifest is locked"}, status: "failed", error: "project manifest is locked"},
		{name: "registered", fields: map[string]any{"registration_status": "registered", "registration_error": nil}, status: "registered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := map[string]any{"task_id": "fold-1", "kind": "structure", "status": "succeeded", "error": nil}
			for key, value := range tc.fields {
				raw[key] = value
			}
			var task Task
			if err := Decode(raw, &task); err != nil {
				t.Fatal(err)
			}
			if task.RegistrationStatus != tc.status || task.RegistrationError != tc.error {
				t.Errorf("registration = %q / %q, want %q / %q", task.RegistrationStatus, task.RegistrationError, tc.status, tc.error)
			}
			if task.Status != "succeeded" || task.Active() || task.Error != nil || task.ErrorText() != "" {
				t.Errorf("registration changed the computation outcome: %+v", task)
			}

			// A computation error remains independent when both fields exist.
			raw["status"], raw["error"] = "failed", map[string]any{"message": "fold engine error"}
			var failed Task
			if err := Decode(raw, &failed); err != nil {
				t.Fatal(err)
			}
			if failed.ErrorText() != "fold engine error" || failed.RegistrationStatus != tc.status || failed.RegistrationError != tc.error {
				t.Errorf("computation and registration errors were mixed: %+v", failed)
			}
		})
	}
}
