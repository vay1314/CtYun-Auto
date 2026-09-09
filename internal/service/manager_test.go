package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/vay1314/CtYun-Keeper/internal/ctyun"
	"github.com/vay1314/CtYun-Keeper/internal/storage"
)

func TestScheduledTaskKey(t *testing.T) {
	if got := scheduledTaskKey("pc"); got != "usage" {
		t.Fatalf("scheduledTaskKey(pc) = %q", got)
	}
	if got := scheduledTaskKey("chat"); got != "chat" {
		t.Fatalf("scheduledTaskKey(chat) = %q", got)
	}
}

func TestStatusUpdatedToday(t *testing.T) {
	location := time.FixedZone("CST", 8*60*60)
	now := time.Date(2026, 9, 9, 0, 5, 0, 0, location)
	if !statusUpdatedToday("2026-09-08T16:01:00Z", now) {
		t.Fatal("UTC timestamp on the current local day should be current")
	}
	if statusUpdatedToday("2026-09-08T15:59:59Z", now) {
		t.Fatal("timestamp from the previous local day should be stale")
	}
}

func TestValidateRedeemReward(t *testing.T) {
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.Local)
	if err := validateRedeemReward(ctyun.Reward{
		ProductID: 99, ProductType: "gift", CostPoints: 300, Status: 2,
		EffectiveAt: "2026-09-01", ExpiresAt: "2026-09-09",
	}, now); err != nil {
		t.Fatalf("reward should remain valid through its expiration date: %v", err)
	}
	for _, reward := range []ctyun.Reward{
		{ProductID: 99, ProductType: "gift", CostPoints: 300, Status: 3},
		{ProductID: 99, ProductType: "gift", CostPoints: 300, Status: 2, EffectiveAt: "2026-09-10"},
		{ProductID: 99, ProductType: "gift", CostPoints: 300, Status: 2, ExpiresAt: "2026-09-08"},
	} {
		if err := validateRedeemReward(reward, now); err == nil {
			t.Fatalf("reward should be rejected: %#v", reward)
		}
	}
}

func TestMonthlyRedeemDaysSupportsLastDay(t *testing.T) {
	got, err := monthlyRedeemDays("1, 15, -1,15")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 1 || got[1] != 15 || got[2] != -1 {
		t.Fatalf("monthlyRedeemDays() = %v", got)
	}
	if _, err = monthlyRedeemDays("0,32"); err == nil {
		t.Fatal("invalid monthly days should fail")
	}
}

func TestResolveRedeemRequiresPendingAndRecordsSuccessDate(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := New(store, nil, dir, "")
	defer manager.Close()
	accountID, err := store.SaveAccount(storage.Account{Name: "test", Username: "user", DeviceCode: "device"}, "password", nil, func(value string, _ []byte) (string, error) {
		return value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.DB.Exec(`INSERT INTO redeem_states(account_id,last_attempt_date,last_attempt_status,last_redeem_times,last_points_spent,message,updated_at) VALUES(?,'2026-09-09','pending',2,600,'pending',?)`, accountID, storage.Now()); err != nil {
		t.Fatal(err)
	}
	if err = manager.ResolveRedeem(accountID, true); err != nil {
		t.Fatal(err)
	}
	var status, successDate string
	var times, points int
	if err = store.DB.QueryRow(`SELECT last_attempt_status,last_success_date,last_redeem_times,last_points_spent FROM redeem_states WHERE account_id=?`, accountID).Scan(&status, &successDate, &times, &points); err != nil {
		t.Fatal(err)
	}
	if status != "success" || successDate != "2026-09-09" || times != 2 || points != 600 {
		t.Fatalf("resolved state = %q %q %d %d", status, successDate, times, points)
	}
	if err = manager.ResolveRedeem(accountID, true); err == nil {
		t.Fatal("resolving a non-pending order should fail")
	}
}

func TestStartTaskRejectsReservedTaskKey(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := New(store, nil, dir, "")
	defer manager.Close()
	manager.starting["1:redeem"] = struct{}{}
	if _, err = manager.StartTask(1, "redeem", "test"); err == nil {
		t.Fatal("a task being created must block a duplicate start")
	}
}
