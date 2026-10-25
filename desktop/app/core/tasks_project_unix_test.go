//go:build !windows

package core

import "testing"

// A live task asked about under another project is not found there, and
// cancelling it from there does not kill it.
func TestStatusAndCancelCheckTheProject(t *testing.T) {
	s := testScheduler(t, 4, 4000)
	a := tempProject(t)
	b := tempProject(t)
	id, _ := submitSleeper(t, s, a, Budget{1, 100}, nil)
	t.Cleanup(func() { _, _ = s.Cancel(id, a) })
	if _, err := s.Status(id, b); err == nil {
		t.Errorf("task of project a returned under project b")
	}
	if c, err := s.Cancel(id, b); err == nil && c["status"] == "cancelled" {
		t.Errorf("cancel under project b killed project a's task")
	}
	if live, err := s.Status(id, a); err != nil || live["status"] != "running" {
		t.Errorf("project a's task: %v %v", live["status"], err)
	}
}
