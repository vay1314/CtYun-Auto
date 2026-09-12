package update

import "testing"

func TestManagerSerializesUpdates(t *testing.T) {
	manager := NewManager()
	if err := manager.Begin(); err != nil {
		t.Fatal(err)
	}
	if err := manager.Begin(); err == nil {
		t.Fatal("second concurrent update was accepted")
	}
	manager.SetProgress(Progress{Status: StatusDownloading, Percent: 50, Received: 5, Total: 10})
	if got := manager.Progress(); got.Status != StatusDownloading || got.Percent != 50 {
		t.Fatalf("unexpected progress: %+v", got)
	}
	manager.Finish()
	if err := manager.Begin(); err != nil {
		t.Fatalf("update could not restart after finish: %v", err)
	}
}
