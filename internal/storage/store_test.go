package storage

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestOpenAndMigrate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, table := range []string{"accounts", "task_runs", "account_platform_status", "redeem_configs", "redeem_states"} {
		var name string
		if err := s.DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name); err != nil {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
}

func TestPurgeCompletedRunsBeforeKeepsActiveRuns(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	insert := func(status, started, finished, path string) {
		t.Helper()
		if _, err := s.DB.Exec(`INSERT INTO task_runs(account_id,task_type,trigger_source,status,started_at,finished_at,log_path) VALUES(NULL,'login','test',?,?,?,?)`, status, started, finished, path); err != nil {
			t.Fatal(err)
		}
	}
	insert("success", "2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z", "old.log")
	insert("success", "2026-09-01T00:00:00Z", "2026-09-01T00:01:00Z", "new.log")
	insert("running", "2026-01-01T00:00:00Z", "", "active.log")
	logs, err := s.RunLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 3 || logs[0].LogPath != "active.log" {
		t.Fatalf("run logs = %#v", logs)
	}

	paths, err := s.PurgeCompletedRunsBefore("2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(paths, []string{"old.log"}) {
		t.Fatalf("purged paths = %v", paths)
	}
	runs, err := s.Runs(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].LogPath != "active.log" || runs[1].LogPath != "new.log" {
		t.Fatalf("remaining runs = %#v", runs)
	}
}
