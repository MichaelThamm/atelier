package tui

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestEnsureInitOnce_concurrentCallersRunInitOnce is the regression test for
// the clobbering this guard prevents: overlapping `terraform init` runs in one
// wrapper corrupt each other's git packfiles.
func TestEnsureInitOnce_concurrentCallersRunInitOnce(t *testing.T) {
	var g initGuard
	var runs atomic.Int32

	release := make(chan struct{})
	runInit := func(context.Context, bool) error {
		runs.Add(1)
		<-release // hold the leader inside init so the rest pile up
		return nil
	}

	const callers = 8
	var wg sync.WaitGroup
	errs := make([]error, callers)
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			<-start
			errs[n] = ensureInitOnce(context.Background(), &g, runInit)
		}(i)
	}

	close(start)
	// Let the leader reach runInit before releasing, so the other callers are
	// genuinely concurrent rather than arriving after init already finished.
	waitFor(t, func() bool { return runs.Load() == 1 })
	close(release)
	wg.Wait()

	if got := runs.Load(); got != 1 {
		t.Errorf("terraform init ran %d times; want exactly 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v; want nil", i, err)
		}
	}
}

// TestEnsureInitOnce_secondCallIsFastPath covers the common case: after init
// succeeded, a later caller must not wait on or re-run anything.
func TestEnsureInitOnce_secondCallIsFastPath(t *testing.T) {
	var g initGuard
	var runs atomic.Int32
	runInit := func(context.Context, bool) error { runs.Add(1); return nil }

	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("terraform init ran %d times; want 1", got)
	}
}

// TestEnsureInitOnce_waiterSeesLeaderError checks a waiter reports the
// leader's failure rather than a nil that would let it plan against a
// half-initialised wrapper.
func TestEnsureInitOnce_waiterSeesLeaderError(t *testing.T) {
	var g initGuard
	boom := errors.New("terraform init: module download failed")
	release := make(chan struct{})
	var runs atomic.Int32

	runInit := func(context.Context, bool) error {
		runs.Add(1)
		<-release
		return boom
	}

	leaderDone := make(chan error, 1)
	go func() { leaderDone <- ensureInitOnce(context.Background(), &g, runInit) }()
	waitFor(t, func() bool { return runs.Load() == 1 })

	waiterDone := make(chan error, 1)
	go func() { waiterDone <- ensureInitOnce(context.Background(), &g, runInit) }()

	// The waiter must still be blocked: it cannot return nil while init runs.
	select {
	case err := <-waiterDone:
		t.Fatalf("waiter returned %v while init was in flight; want it to wait", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-leaderDone; !errors.Is(err, boom) {
		t.Errorf("leader err = %v; want %v", err, boom)
	}
	if err := <-waiterDone; !errors.Is(err, boom) {
		t.Errorf("waiter err = %v; want the leader's %v", err, boom)
	}
	if got := runs.Load(); got != 1 {
		t.Errorf("terraform init ran %d times; want 1", got)
	}
}

// TestEnsureInitOnce_retryAfterFailure checks a failed init does not latch:
// the next caller tries again rather than reporting success forever.
func TestEnsureInitOnce_retryAfterFailure(t *testing.T) {
	var g initGuard
	var runs atomic.Int32
	runInit := func(context.Context, bool) error {
		if runs.Add(1) == 1 {
			return errors.New("transient")
		}
		return nil
	}

	if err := ensureInitOnce(context.Background(), &g, runInit); err == nil {
		t.Fatal("first call should fail")
	}
	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatalf("second call should retry and succeed, got %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("terraform init ran %d times; want 2", got)
	}
}

// TestEnsureInitOnce_resetInvalidatesInFlightInit is the ref-switch case: an
// init already running described the old module source, so its success must
// not mark the wrapper ready, or the next plan would use the stale cache.
func TestEnsureInitOnce_resetInvalidatesInFlightInit(t *testing.T) {
	var g initGuard
	var runs atomic.Int32

	release := make(chan struct{})
	runInit := func(context.Context, bool) error {
		runs.Add(1)
		<-release
		return nil
	}

	leaderDone := make(chan error, 1)
	go func() { leaderDone <- ensureInitOnce(context.Background(), &g, runInit) }()
	waitFor(t, func() bool { return runs.Load() == 1 })

	// A ref switch lands mid-init.
	g.reset()

	close(release)
	if err := <-leaderDone; err != nil {
		t.Fatalf("leader err = %v; want nil (its own init did succeed)", err)
	}

	// The next caller must re-init rather than trust the invalidated run.
	release2 := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		second <- ensureInitOnce(context.Background(), &g, func(context.Context, bool) error {
			runs.Add(1)
			<-release2
			return nil
		})
	}()
	waitFor(t, func() bool { return runs.Load() == 2 })
	close(release2)
	if err := <-second; err != nil {
		t.Fatalf("re-init after reset: %v", err)
	}
	if got := runs.Load(); got != 2 {
		t.Errorf("terraform init ran %d times; want 2 (the reset must force a re-init)", got)
	}
}

// TestEnsureInitOnce_resetRequestsUpgradeOnce checks a ref switch turns the
// next run into `init -upgrade` and only the next one. The -upgrade is what
// re-fetches a module whose ?ref= changed under an unchanged source URL, so
// losing it would plan against the old revision.
func TestEnsureInitOnce_resetRequestsUpgradeOnce(t *testing.T) {
	var g initGuard
	var upgrades []bool
	runInit := func(_ context.Context, upgrade bool) error {
		upgrades = append(upgrades, upgrade)
		return nil
	}

	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatal(err)
	}
	g.reset()
	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatal(err)
	}
	// The -upgrade succeeded, so the wrapper is initialised again and this
	// must be a fast path rather than a third run.
	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatal(err)
	}

	want := []bool{false, true}
	if len(upgrades) != len(want) {
		t.Fatalf("ran init %d times (%v); want %d (the third call must be a fast path)",
			len(upgrades), upgrades, len(want))
	}
	for i := range want {
		if upgrades[i] != want[i] {
			t.Errorf("run %d upgrade = %v; want %v (all runs: %v)", i, upgrades[i], want[i], upgrades)
		}
	}
}

// TestEnsureInitOnce_failedUpgradeRetriesUpgrade checks a reset is not
// consumed by a run that failed: the next attempt must still be an -upgrade,
// or a transient module-download failure would silently degrade to a plain
// init that cannot see the new revision.
func TestEnsureInitOnce_failedUpgradeRetriesUpgrade(t *testing.T) {
	var g initGuard
	var upgrades []bool
	runInit := func(_ context.Context, upgrade bool) error {
		upgrades = append(upgrades, upgrade)
		if len(upgrades) == 1 {
			return errors.New("module download failed")
		}
		return nil
	}

	g.reset() // a ref switch, so the first run is an -upgrade
	if err := ensureInitOnce(context.Background(), &g, runInit); err == nil {
		t.Fatal("first -upgrade should fail")
	}
	if err := ensureInitOnce(context.Background(), &g, runInit); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(upgrades) != 2 || !upgrades[0] || !upgrades[1] {
		t.Errorf("upgrades = %v; want both runs to be -upgrade", upgrades)
	}
}

// TestEnsureInitOnce_waiterContextCancel checks a caller that gives up waiting
// returns promptly instead of blocking on the leader.
func TestEnsureInitOnce_waiterContextCancel(t *testing.T) {
	var g initGuard
	release := make(chan struct{})
	var runs atomic.Int32
	runInit := func(context.Context, bool) error {
		runs.Add(1)
		<-release
		return nil
	}

	go func() { _ = ensureInitOnce(context.Background(), &g, runInit) }()
	waitFor(t, func() bool { return runs.Load() == 1 })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := ensureInitOnce(ctx, &g, runInit)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v; want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("waited %s; want to give up promptly", elapsed)
	}
	close(release)
}

// waitFor polls cond until it holds or the test times out. Condition reads here
// are all atomic counters, so the polling cannot race.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the leader to start init")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
