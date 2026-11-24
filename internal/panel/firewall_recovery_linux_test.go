package panel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFirewallRecoveryRetainsFailedTransaction(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.data = t.TempDir()
	const id = "0123456789abcdef"
	dir := filepath.Join(cfg.data, "firewall", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(pendingPath(), []byte("{invalid"))
	if err := recoverFirewallPending(context.Background()); err == nil {
		t.Fatal("corrupted recovery record was ignored")
	}
	data, err := json.Marshal(firewallPending{ID: id, Unit: "ownpanel-fw-" + id, Expires: time.Now().Add(-time.Minute), Durable: true})
	if err != nil {
		t.Fatal(err)
	}
	write(pendingPath(), data)
	script := filepath.Join(dir, "restore.sh")
	write(script, []byte("exit 1\n"))
	if err := recoverFirewallPending(context.Background()); err == nil {
		t.Fatal("failed rollback was reported as successful")
	}
	if pending, err := readPending(); err != nil || pending == nil {
		t.Fatalf("failed rollback lost transaction: %v", err)
	}
	write(script, []byte("exit 0\n"))
	if err := recoverFirewallPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if pending, err := readPending(); err != nil || pending != nil {
		t.Fatalf("successful retry left transaction: %v", err)
	}
}

func TestFirewallConfirmationSharesRecoveryLock(t *testing.T) {
	old := cfg
	t.Cleanup(func() { cfg = old })
	cfg.data = t.TempDir()
	pending := &firewallPending{ID: "fedcba9876543210", Unit: "ownpanel-fw-fedcba9876543210", Expires: time.Now().Add(time.Minute), Durable: true, ConfirmGuard: true}
	dir := filepath.Join(cfg.data, "firewall", pending.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "restore.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := markFirewallConfirmed(ctx, pending); err == nil {
		t.Fatal("confirmation raced an active recovery lock")
	}
	if _, err := os.Stat(filepath.Join(dir, "confirmed")); !os.IsNotExist(err) {
		t.Fatalf("blocked confirmation wrote its decision: %v", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := markFirewallConfirmed(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(pending)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pendingPath(), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "restore.sh"), []byte("exit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := recoverFirewallPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p, err := readPending(); err != nil || p != nil {
		t.Fatalf("confirmed transaction remained pending: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "restored"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := markFirewallConfirmed(context.Background(), pending); err == nil {
		t.Fatal("recovered transaction was confirmed")
	}
}
