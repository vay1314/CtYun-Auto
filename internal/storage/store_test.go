package storage

import (
	"path/filepath"
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
