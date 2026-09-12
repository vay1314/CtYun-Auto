package service

import (
	"context"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
	"path/filepath"
	"testing"
	"time"
)

func TestUpdateWaitsForStartingTask(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := New(store, nil, dir, "")
	defer m.Close()
	m.starting["1:redeem"] = struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := m.PrepareForUpdate(ctx); err == nil {
		t.Fatal("update ignored a starting task")
	}
	if m.maintenance {
		t.Fatal("failed preparation left maintenance enabled")
	}
}

func TestUpdateDoesNotCancelKeepaliveWhenUsageBlocks(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := New(store, nil, dir, "")
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.clients[1] = &clientState{ctx: ctx, cancel: cancel}
	m.active[1] = running{typ: "1:pc", cancel: func() {}}
	if err := m.PrepareForUpdate(context.Background()); err == nil {
		t.Fatal("usage did not block update")
	}
	if ctx.Err() != nil {
		t.Fatal("rejected update cancelled live usage connection")
	}
}

func TestScheduledTaskReleasesClaimWhenUpdateMaintenanceRejectsStart(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := New(store, nil, dir, "")
	defer m.Close()
	claimKey := "202609121200"
	if !store.Claim(1, "login", claimKey) {
		t.Fatal("failed to create initial scheduler claim")
	}
	m.maintenance = true
	m.startScheduledTask(storage.Account{ID: 1, Name: "test"}, "login", time.Now(), claimKey)
	if !store.Claim(1, "login", claimKey) {
		t.Fatal("maintenance rejection consumed the scheduler claim")
	}
}
