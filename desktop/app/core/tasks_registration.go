package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Registration is a separate, durable transaction from child execution.
// The pending receipt is written before Submit, and acknowledged only after
// register_task_run commits project.json. Retrying that operation is an
// idempotent upsert; it never launches the task again. Keeping the receipt
// outside status.json also lets another Service repair it without racing
// the original scheduler's in-memory execution snapshot.
const registrationFile = "registration.json"

type taskRegistration struct {
	Status string  `json:"status"`
	Error  *string `json:"error"`
}

func readRegistration(runDir string) (taskRegistration, error) {
	var registration taskRegistration
	data, err := readProjectFile(filepath.Join(runDir, registrationFile))
	if err != nil {
		return registration, err
	}
	if err := json.Unmarshal(data, &registration); err != nil {
		return registration, err
	}
	if !slices.Contains([]string{"pending", "failed", "registered"}, registration.Status) {
		return registration, errors.New("invalid task registration status")
	}
	return registration, nil
}

// withRegistration reports registration independently from execution.
// A missing receipt is unknown (older tasks), never evidence of success.
func withRegistration(value Obj) Obj {
	value["registration_status"], value["registration_error"] = nil, nil
	if dir, ok := value["run_dir"].(string); ok {
		registration, err := readRegistration(dir)
		if err == nil {
			value["registration_status"] = registration.Status
			if registration.Error != nil {
				value["registration_error"] = *registration.Error
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			value["registration_status"] = "failed"
			value["registration_error"] = "cannot read task registration: " + err.Error()
		}
	}
	return value
}

// registerTask returns whether project.json may have changed. The OS lock
// serializes a finish callback with reopen recovery, including other app
// instances. An interrupted attempt leaves pending/failed for the next open.
func (s *Service) registerTask(rt *Runtime, project, id string) (bool, error) {
	if err := ValidateID(id); err != nil {
		return false, err
	}
	runDir, err := projectRunDir(id, project)
	if err != nil {
		return false, err
	}
	if info, err := os.Lstat(runDir); err != nil || !info.IsDir() {
		return false, errors.New("task run directory does not exist")
	}
	lock, err := tryFileLock(filepath.Join(runDir, "registration.lock"))
	if errors.Is(err, errLockBusy) {
		return false, nil // another callback/recovery owns the transaction
	}
	if err != nil {
		return false, err
	}
	defer lock.Close()
	snap, err := s.tasks.statusRaw(id, project)
	if err != nil {
		return false, err
	}
	state, _ := snap["status"].(string)
	if !slices.Contains([]string{"succeeded", "failed", "cancelled", "interrupted"}, state) {
		return false, nil
	}
	// register_task_run reads status.json, not this snapshot: while the
	// final status is unsaved the receipt stays pending (the scheduler
	// defers onFinish until its retry saves the status; else the next
	// open's recovery registers it).
	if disk, err := readStatusFile(id, runDir); err != nil || statusActive(disk) {
		return false, nil
	}
	registration, err := readRegistration(runDir)
	if err == nil && registration.Status == "registered" {
		return false, nil
	}
	if err := writeAtomic(runDir, registrationFile, taskRegistration{Status: "pending"}); err != nil {
		return false, fmt.Errorf("cannot save pending task registration: %w", err)
	}
	_, registerErr := rt.Call(Control, Obj{"operation": "register_task_run", "path": project, "task_id": id})
	registration = taskRegistration{Status: "registered"}
	if registerErr != nil {
		message := registerErr.Error()
		registration.Status, registration.Error = "failed", &message
	}
	// If this acknowledgement fails, the durable pending receipt still
	// causes an idempotent retry (including a lost successful response).
	return registerErr == nil, errors.Join(registerErr, writeAtomic(runDir, registrationFile, registration))
}

// recoverRegistrations reconciles terminal tasks, including old records
// without receipts. Failures do not prevent opening a readable project;
// their receipt/error remains visible and the next open retries them.
func (s *Service) recoverRegistrations(project string) bool {
	rt, err := s.runtime()
	if err != nil {
		return false
	}
	tasks, err := s.tasks.List(project)
	if err != nil {
		s.logf("HYB2: cannot recover task registrations: %v", err)
		return false
	}
	changed := false
	for _, task := range tasks {
		state, _ := task["status"].(string)
		if task["registration_status"] == "registered" ||
			!slices.Contains([]string{"succeeded", "failed", "cancelled", "interrupted"}, state) {
			continue
		}
		id, _ := task["task_id"].(string)
		registered, err := s.registerTask(rt, project, id)
		changed = changed || registered
		if err != nil {
			s.logf("HYB2: register_task_run recovery failed for %s: %v", id, err)
		}
	}
	return changed
}
