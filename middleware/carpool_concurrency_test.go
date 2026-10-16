package middleware

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

type carpoolTestRig struct {
	l     *carpoolLimiter
	base  time.Time
	clock atomic.Int64
	raw   atomic.Value
	seq   atomic.Int64
	mu    sync.Mutex
	logs  []string
}

func newCarpoolTestRig(t *testing.T) *carpoolTestRig {
	t.Helper()
	h := &carpoolTestRig{l: newCarpoolLimiter(), base: time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)}
	h.clock.Store(h.base.UnixNano())
	h.raw.Store("")
	h.l.now = func() time.Time { return time.Unix(0, h.clock.Load()).UTC() }
	h.l.readPolicy = func() string { return h.raw.Load().(string) }
	h.l.checkInterval = 5 * time.Millisecond
	h.l.log = func(message string) {
		h.mu.Lock()
		h.logs = append(h.logs, message)
		h.mu.Unlock()
	}
	return h
}

func (h *carpoolTestRig) policy(mode string, limit, wait int) map[string]any {
	return map[string]any{
		"version": 1, "issued_at": h.base.Add(time.Duration(h.seq.Add(1)) * time.Microsecond).Format(time.RFC3339Nano),
		"mode": mode, "wait_seconds": wait, "expires_at": h.base.Add(2 * time.Hour).Format(time.RFC3339Nano),
		"user_limits": map[string]any{"741": limit},
	}
}

func carpoolJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := common.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (h *carpoolTestRig) set(t *testing.T, mode string, limit, wait int) {
	t.Helper()
	h.raw.Store(carpoolJSON(t, h.policy(mode, limit, wait)))
}

func (h *carpoolTestRig) advance(d time.Duration) { h.clock.Add(int64(d)) }

func (h *carpoolTestRig) logCount(fragment string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, message := range h.logs {
		if strings.Contains(message, fragment) {
			n++
		}
	}
	return n
}

func (h *carpoolTestRig) counts() (int, int, int) {
	h.l.mu.Lock()
	defer h.l.mu.Unlock()
	var running, queued int
	for _, user := range h.l.users {
		running += user.inflight
		queued += len(user.waiters)
	}
	return running, queued, len(h.l.users)
}

func (h *carpoolTestRig) assertCounts(t *testing.T, running, queued, users int) {
	t.Helper()
	r, q, u := h.counts()
	if r != running || q != queued || u != users {
		t.Fatalf("counts = (%d,%d,%d), want (%d,%d,%d)", r, q, u, running, queued, users)
	}
}

func carpoolEventually(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !check() {
		select {
		case <-deadline.C:
			t.Fatal("condition not satisfied within 1 second")
		case <-tick.C:
		}
	}
}

func TestCarpoolPolicyValidation(t *testing.T) {
	h := newCarpoolTestRig(t)
	for _, raw := range []string{"", "{", "null", "[]", "1", `"x"`, "true", "{}"} {
		t.Run("raw="+raw, func(t *testing.T) {
			if mode, _ := parseCarpoolPolicy(raw).forUser(741, h.l.now()); mode != "off" {
				t.Fatal("invalid JSON policy enabled limiting")
			}
		})
	}
	cases := []struct {
		key   string
		value any
	}{
		{"version", nil}, {"version", 2}, {"version", 1.5}, {"version", "1"},
		{"issued_at", nil}, {"issued_at", "bad"}, {"issued_at", 1},
		{"expires_at", nil}, {"expires_at", "bad"}, {"expires_at", h.base.Format(time.RFC3339Nano)},
		{"expires_at", h.base.Add(-time.Second).Format(time.RFC3339Nano)},
		{"mode", nil}, {"mode", "ENFORCE"}, {"mode", "bad"},
		{"wait_seconds", 1.5}, {"wait_seconds", nil}, {"wait_seconds", "10"},
		{"user_limits", []int{1}}, {"user_limits", "bad"},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprintf("%s/%d", tc.key, i), func(t *testing.T) {
			value := h.policy("enforce", 2, 10)
			if tc.value == nil {
				delete(value, tc.key)
			} else {
				value[tc.key] = tc.value
			}
			if tc.key == "wait_seconds" && tc.value == nil {
				value[tc.key] = nil // Missing wait_seconds defaults to 10, null is invalid.
			}
			p := parseCarpoolPolicy(carpoolJSON(t, value))
			if mode, _ := p.forUser(741, h.l.now()); mode != "off" {
				t.Fatalf("invalid %s accepted", tc.key)
			}
		})
	}
	for _, wait := range []int{-100, 0, 10, 60, 100} {
		p := parseCarpoolPolicy(carpoolJSON(t, h.policy("enforce", 1, wait)))
		if p.wait != time.Duration(max(0, min(60, wait)))*time.Second {
			t.Fatalf("wait not clamped: %v", p.wait)
		}
	}
	for _, tc := range []struct {
		literal string
		want    time.Duration
	}{
		{"9999999999999999999999999999999999", 60 * time.Second},
		{"-9999999999999999999999999999999999", 0},
	} {
		raw := carpoolJSON(t, h.policy("enforce", 1, 10))
		raw = strings.Replace(raw, `"wait_seconds":10`, `"wait_seconds":`+tc.literal, 1)
		p := parseCarpoolPolicy(raw)
		if mode, _ := p.forUser(741, h.l.now()); mode != "enforce" || p.wait != tc.want {
			t.Fatal("overflowing integer was not clamped")
		}
	}
	value := h.policy("enforce", 1, 10)
	delete(value, "wait_seconds")
	if p := parseCarpoolPolicy(carpoolJSON(t, value)); p.wait != 10*time.Second {
		t.Fatal("missing wait should default to 10 seconds")
	}
	value["user_limits"] = map[string]any{
		"741": 4, "742": 64, "743": 1, "0": 1, "-1": 2, "abc": 1,
		"999999999999999999999999": 1, "744": 0, "745": 65, "746": 1.5,
		"747": "2", "748": nil, "749": true, "750": map[string]int{"limit": 1},
	}
	p := parseCarpoolPolicy(carpoolJSON(t, value))
	if len(p.userLimits) != 3 || p.userLimits[741] != 4 || p.userLimits[742] != 64 || p.userLimits[743] != 1 {
		t.Fatalf("entry-level validation: %#v", p.userLimits)
	}
}

func TestCarpoolSnapshotReuseExpiryAndOrder(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	old := h.raw.Load().(string)
	h.l.RefreshAndApply(nil)
	first := h.l.snapshot.Load()
	h.l.RefreshAndApply(nil)
	if h.l.snapshot.Load() != first {
		t.Fatal("unchanged raw reparsed")
	}
	h.advance(2 * time.Hour)
	h.l.RefreshAndApply(nil)
	if h.l.snapshot.Load() != first {
		t.Fatal("expiry should not reparse")
	}
	if mode, _ := first.forUser(741, h.l.now()); mode != "off" {
		t.Fatal("cached snapshot did not expire")
	}
	h.clock.Store(h.base.UnixNano())
	h.set(t, "off", 1, 10)
	h.l.RefreshAndApply(nil)
	off := h.l.snapshot.Load()
	h.raw.Store(old)
	h.l.RefreshAndApply(nil)
	if h.l.snapshot.Load() != off || h.l.snapshot.Load().mode != "off" {
		t.Fatal("old issued_at resurrected enforce after off")
	}
	equal := h.policy("enforce", 1, 10)
	equal["issued_at"] = off.issuedAt.Format(time.RFC3339Nano)
	h.raw.Store(carpoolJSON(t, equal))
	h.l.RefreshAndApply(nil)
	if h.l.snapshot.Load() != off {
		t.Fatal("equal issued_at replaced the policy")
	}
	h.raw.Store("invalid")
	h.l.RefreshAndApply(nil)
	invalid := h.l.snapshot.Load()
	h.raw.Store(old)
	h.l.RefreshAndApply(nil)
	if h.l.snapshot.Load() != invalid {
		t.Fatal("invalid policy reset the high-water mark")
	}
}

func TestCarpoolConcurrentRefreshDoesNotRegress(t *testing.T) {
	h := newCarpoolTestRig(t)
	old := carpoolJSON(t, h.policy("enforce", 1, 10))
	newer := carpoolJSON(t, h.policy("off", 1, 10))
	entered, resume := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h.l.readPolicy = func() string {
		if calls.Add(1) == 1 {
			close(entered)
			<-resume // Hold the older read while a newer refresh starts.
			return old
		}
		return newer
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); h.l.RefreshAndApply(nil) }()
	<-entered
	go func() { defer wg.Done(); h.l.RefreshAndApply(nil) }()
	close(resume)
	wg.Wait()
	if p := h.l.snapshot.Load(); p.mode != "off" || p.raw != newer {
		t.Fatal("concurrent refresh regressed")
	}
}

func TestCarpoolHandoffDeadline(t *testing.T) {
	for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(delta.String(), func(t *testing.T) {
			h := newCarpoolTestRig(t)
			h.set(t, "enforce", 1, 10)
			a := h.l.acquire(context.Background(), 741, "/hold")
			w := h.l.acquire(context.Background(), 741, "/wait").waiter
			h.advance(10*time.Second + delta)
			h.l.release(a.lease)
			h.advance(time.Second) // Acceptance may happen after the deadline.
			state, lease, _ := h.l.resolve(w)
			if delta < 0 {
				if state != carpoolAccepted || lease.id == 0 {
					t.Fatal("reservation before deadline was lost")
				}
				h.l.release(lease)
			} else if state != carpoolTimedOut || lease.id != 0 {
				t.Fatal("handoff at/after deadline was accepted")
			}
			h.assertCounts(t, 0, 0, 0)
		})
	}
}

func TestCarpoolGrantedCancellationAndFIFO(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	first := h.l.acquire(context.Background(), 741, "/first")
	ctx, cancel := context.WithCancel(context.Background())
	w1 := h.l.acquire(ctx, 741, "/second").waiter
	h.l.release(first.lease)
	if w1.state != carpoolGranted {
		t.Fatal("waiter was not granted a reservation")
	}
	w2 := h.l.acquire(context.Background(), 741, "/third").waiter
	cancel() // The notify is already ready; cancellation must still win.
	state, lease, _ := h.l.resolve(w1)
	if state != carpoolCancelled || lease.id != 0 {
		t.Fatal("cancelled reservation was accepted")
	}
	state, lease, _ = h.l.resolve(w2)
	if state != carpoolAccepted || lease.id == 0 {
		t.Fatal("cancelled reservation was not handed to the next waiter")
	}
	h.l.release(lease)
	h.l.release(lease)
	h.l.release(first.lease)
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolFIFOSkipsCancelledAndExpired(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 4, 10)
	var holders []carpoolLeaseRef
	for i := 0; i < 4; i++ {
		holders = append(holders, h.l.acquire(context.Background(), 741, "/hold").lease)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelled := h.l.acquire(ctx, 741, "/cancelled").waiter
	expired := h.l.acquire(context.Background(), 741, "/expired").waiter
	h.advance(time.Second)
	w1 := h.l.acquire(context.Background(), 741, "/first").waiter
	w2 := h.l.acquire(context.Background(), 741, "/second").waiter
	cancel()
	h.advance(9 * time.Second)
	h.l.release(holders[0])
	if cancelled.state != carpoolCancelled || expired.state != carpoolTimedOut || w1.state != carpoolGranted || w2.state != carpoolWaiting {
		t.Fatalf("FIFO states: %d %d %d %d", cancelled.state, expired.state, w1.state, w2.state)
	}
	_, lease1, _ := h.l.resolve(w1)
	h.l.release(lease1)
	state, lease2, _ := h.l.resolve(w2)
	if state != carpoolAccepted {
		t.Fatal("second live waiter did not receive the next slot")
	}
	h.l.release(lease2)
	for _, holder := range holders {
		h.l.release(holder)
	}
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolCancelReleaseRace(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	for i := 0; i < 200; i++ {
		a := h.l.acquire(context.Background(), 741, "/hold")
		ctx, cancel := context.WithCancel(context.Background())
		w := h.l.acquire(ctx, 741, "/wait").waiter
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() { defer wg.Done(); <-start; cancel() }()
		go func() { defer wg.Done(); <-start; h.l.release(a.lease) }()
		close(start)
		wg.Wait()
		state, lease, _ := h.l.resolve(w)
		if state != carpoolCancelled || lease.id != 0 {
			t.Fatal("cancellation lost to reservation")
		}
		h.assertCounts(t, 0, 0, 0)
	}
}

func TestCarpoolTimeoutReleaseRace(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	for i := 0; i < 200; i++ {
		a := h.l.acquire(context.Background(), 741, "/hold")
		w := h.l.acquire(context.Background(), 741, "/wait").waiter
		h.advance(10 * time.Second)
		var wg sync.WaitGroup
		wg.Add(2)
		var state carpoolWaiterState
		go func() { defer wg.Done(); state, _, _ = h.l.resolve(w) }()
		go func() { defer wg.Done(); h.l.release(a.lease) }()
		wg.Wait()
		if state != carpoolTimedOut {
			t.Fatal("late handoff beat timeout")
		}
		h.assertCounts(t, 0, 0, 0)
	}
}

func TestCarpoolLongLeaseWarnsOnceWithoutReclaim(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	a := h.l.acquire(context.Background(), 741, "/hold")
	h.advance(31 * time.Minute)
	h.l.RefreshAndApply(nil)
	h.l.RefreshAndApply(nil)
	h.assertCounts(t, 1, 0, 1)
	if h.logCount("long lease user=741") != 1 {
		t.Fatal("long lease warning not exactly once")
	}
	w := h.l.acquire(context.Background(), 741, "/wait").waiter
	if w == nil {
		t.Fatal("aged lease was reclaimed")
	}
	h.l.release(a.lease)
	_, lease, _ := h.l.resolve(w)
	h.l.release(lease)
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolCleanupChecksIdentity(t *testing.T) {
	h := newCarpoolTestRig(t)
	old := &carpoolUserState{id: 741}
	current := &carpoolUserState{id: 741}
	h.l.mu.Lock()
	h.l.users[741] = current
	h.l.cleanupLocked(old)
	retained := h.l.users[741] == current
	h.l.cleanupLocked(current)
	h.l.mu.Unlock()
	if !retained {
		t.Fatal("stale cleanup removed replacement state")
	}
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolShrinkAndOffDoNotReclaimRunningLeases(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 2, 10)
	a := h.l.acquire(context.Background(), 741, "/a")
	b := h.l.acquire(context.Background(), 741, "/b")
	h.set(t, "enforce", 1, 10)
	w := h.l.acquire(context.Background(), 741, "/wait").waiter
	h.l.release(a.lease)
	if w.state != carpoolWaiting {
		t.Fatal("shrink handed out a slot above the new limit")
	}
	h.set(t, "off", 1, 10)
	untracked := h.l.acquire(context.Background(), 741, "/off")
	if untracked.lease.id != 0 || untracked.waiter != nil {
		t.Fatal("off request was tracked")
	}
	state, lease, _ := h.l.resolve(w)
	if state != carpoolAccepted || lease.id != 0 {
		t.Fatal("off did not release queued request without counting")
	}
	h.assertCounts(t, 1, 0, 1) // Original running lease lives until handler return.
	h.l.release(b.lease)
	h.set(t, "enforce", 1, 10)
	c := h.l.acquire(context.Background(), 741, "/reenabled")
	if c.lease.id == 0 {
		t.Fatal("off request was counted retroactively")
	}
	h.l.release(c.lease)
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolUsersAreIndependent(t *testing.T) {
	h := newCarpoolTestRig(t)
	p := h.policy("enforce", 1, 10)
	p["user_limits"] = map[string]int{"741": 1, "742": 1}
	h.raw.Store(carpoolJSON(t, p))
	a := h.l.acquire(context.Background(), 741, "/first-user")
	b := h.l.acquire(context.Background(), 742, "/second-user")
	if a.lease.id == 0 || b.lease.id == 0 {
		t.Fatal("different users shared a cap")
	}
	h.assertCounts(t, 2, 0, 2)
	h.l.release(a.lease)
	h.l.release(b.lease)
	h.assertCounts(t, 0, 0, 0)
}
