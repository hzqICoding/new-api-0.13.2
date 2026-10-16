package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const carpoolConcurrencyPolicyKey = "CarpoolConcurrencyPolicy"

// A snapshot is immutable after publication. Invalid policies retain a valid
// issued_at, if available, so even a fail-open update cannot be rolled back.
type carpoolPolicy struct {
	raw        string
	issuedAt   time.Time
	ordered    bool
	expiresAt  time.Time
	mode       string
	wait       time.Duration
	userLimits map[int]int
}

func parseCarpoolPolicy(raw string) *carpoolPolicy {
	p := &carpoolPolicy{raw: raw, mode: "off"}
	var fields map[string]json.RawMessage
	if common.UnmarshalJsonStr(raw, &fields) != nil || fields == nil {
		return p
	}
	var issued string
	if common.Unmarshal(fields["issued_at"], &issued) != nil {
		return p
	}
	var err error
	p.issuedAt, err = time.Parse(time.RFC3339Nano, issued)
	if err != nil {
		return p
	}
	p.ordered = true
	var version int
	var mode, expires string
	if common.Unmarshal(fields["version"], &version) != nil || version != 1 ||
		common.Unmarshal(fields["mode"], &mode) != nil ||
		(mode != "off" && mode != "observe" && mode != "enforce") ||
		common.Unmarshal(fields["expires_at"], &expires) != nil {
		return p
	}
	p.expiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return p
	}
	wait := int64(10)
	if value, ok := fields["wait_seconds"]; ok {
		var seconds json.Number
		if common.GetJsonType(value) != "number" || common.Unmarshal(value, &seconds) != nil {
			return p
		}
		wait, err = seconds.Int64()
		// ParseInt saturates on overflow, which still clamps correctly. Other
		// errors (fractional numbers, for example) invalidate the policy.
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return p
		}
	}
	p.wait = time.Duration(max(0, min(60, wait))) * time.Second
	var entries map[string]json.RawMessage
	if value, ok := fields["user_limits"]; ok && common.Unmarshal(value, &entries) != nil {
		return p
	}
	p.userLimits = make(map[int]int, len(entries))
	for key, value := range entries {
		// Canonical decimal IDs avoid ambiguous aliases (e.g. "01" and "1").
		id, err := strconv.Atoi(key)
		if err != nil || id <= 0 || strconv.Itoa(id) != key {
			continue
		}
		var limit int
		if common.Unmarshal(value, &limit) == nil && limit >= 1 && limit <= 64 {
			p.userLimits[id] = limit
		}
	}
	p.mode = mode
	return p
}

func (p *carpoolPolicy) forUser(id int, now time.Time) (string, int) {
	if p == nil || p.mode == "off" || !now.Before(p.expiresAt) {
		return "off", 0
	}
	limit := p.userLimits[id]
	if limit == 0 {
		return "off", 0
	}
	return p.mode, limit
}

type carpoolWaiterState uint8

const (
	carpoolWaiting carpoolWaiterState = iota
	carpoolGranted
	carpoolAccepted
	carpoolCancelled
	carpoolTimedOut
	carpoolReleasedByPolicy
)

type carpoolLease struct {
	startedAt time.Time
	warned    bool
}

type carpoolUserState struct {
	id       int
	inflight int
	leases   map[uint64]*carpoolLease
	waiters  []*carpoolWaiter
}

type carpoolLeaseRef struct {
	user *carpoolUserState
	id   uint64
}

type carpoolWaiter struct {
	user     *carpoolUserState
	ctx      context.Context
	path     string
	deadline time.Time
	notify   chan struct{}
	state    carpoolWaiterState
	lease    carpoolLeaseRef
	limit    int
}

type carpoolAdmission struct {
	lease     carpoolLeaseRef
	waiter    *carpoolWaiter
	limit     int
	rejected  bool
	cancelled bool
}

type carpoolLimiter struct {
	// Lock order: refreshMu -> OptionMapRWMutex (copy only, then unlock) -> mu.
	// No path holding mu may enter RefreshAndApply. All I/O is outside mu.
	refreshMu sync.Mutex
	snapshot  atomic.Pointer[carpoolPolicy]
	lastRaw   string
	hasRaw    bool
	latest    time.Time
	hasLatest bool
	mu        sync.Mutex
	users     map[int]*carpoolUserState
	nextLease uint64

	// Set at construction; tests can inject a clock, source and short interval.
	now           func() time.Time
	readPolicy    func() string
	log           func(string)
	checkInterval time.Duration
}

func newCarpoolLimiter() *carpoolLimiter {
	l := &carpoolLimiter{
		users: make(map[int]*carpoolUserState), now: time.Now, log: common.SysLog,
		checkInterval: time.Second,
		readPolicy: func() string {
			common.OptionMapRWMutex.RLock()
			raw := common.OptionMap[carpoolConcurrencyPolicyKey]
			common.OptionMapRWMutex.RUnlock()
			return raw
		},
	}
	l.snapshot.Store(&carpoolPolicy{mode: "off"})
	return l
}

// RefreshAndApply serializes reading, parsing, publication and queue updates.
// The optional action runs in the same transaction, so admission/release cannot
// act on a snapshot superseded between refresh and taking the limiter lock.
func (l *carpoolLimiter) RefreshAndApply(action func(*carpoolPolicy, time.Time, *[]string)) {
	var logs []string
	l.refreshMu.Lock()
	raw := l.readPolicy()
	if !l.hasRaw || raw != l.lastRaw {
		p := parseCarpoolPolicy(raw)
		l.lastRaw, l.hasRaw = raw, true
		// Equal timestamps identify the same policy. Preserve the high-water
		// mark across malformed/missing updates as well as expiry.
		if !p.ordered || !l.hasLatest || p.issuedAt.After(l.latest) {
			if p.ordered {
				l.latest, l.hasLatest = p.issuedAt, true
			}
			l.snapshot.Store(p)
		}
	}
	p := l.snapshot.Load()
	l.mu.Lock()
	now := l.now()
	for _, user := range l.users {
		l.applyUserLocked(user, p, now, &logs)
	}
	if action != nil {
		action(p, l.now(), &logs)
	}
	l.mu.Unlock()
	l.refreshMu.Unlock()
	for _, message := range logs {
		l.log(message)
	}
}

func (l *carpoolLimiter) leaseLocked(user *carpoolUserState, now time.Time) carpoolLeaseRef {
	l.nextLease++
	user.leases[l.nextLease] = &carpoolLease{startedAt: now}
	user.inflight++
	return carpoolLeaseRef{user: user, id: l.nextLease}
}

func (l *carpoolLimiter) dropLeaseLocked(ref carpoolLeaseRef) {
	if ref.id == 0 {
		return
	}
	if _, ok := ref.user.leases[ref.id]; ok {
		delete(ref.user.leases, ref.id)
		ref.user.inflight--
	}
}

func (l *carpoolLimiter) cleanupLocked(user *carpoolUserState) {
	if user.inflight == 0 && len(user.waiters) == 0 && l.users[user.id] == user {
		delete(l.users, user.id)
	}
}

func (l *carpoolLimiter) observeLocked(user *carpoolUserState, limit int, path string, logs *[]string) {
	if user.inflight >= limit {
		*logs = append(*logs, fmt.Sprintf("[carpool-concurrency] observe over-limit user=%d inflight=%d limit=%d path=%s", user.id, user.inflight, limit, path))
	}
}

// leaveQueueLocked closes a private channel, never a blocking send. Only the
// waiting state owns a queue entry; granted reservations live in leases.
func (l *carpoolLimiter) leaveQueueLocked(w *carpoolWaiter) {
	for i, candidate := range w.user.waiters {
		if candidate == w {
			copy(w.user.waiters[i:], w.user.waiters[i+1:])
			w.user.waiters[len(w.user.waiters)-1] = nil
			w.user.waiters = w.user.waiters[:len(w.user.waiters)-1]
			break
		}
	}
	close(w.notify)
}

// decideLocked is the sole waiter arbiter, used by handoff and the waking
// request. Cancellation wins until acceptance, including after reservation.
// A reservation made before the deadline remains valid after the deadline.
func (l *carpoolLimiter) decideLocked(w *carpoolWaiter, accept bool, p *carpoolPolicy, logs *[]string) {
	if w.state == carpoolAccepted || w.state == carpoolCancelled || w.state == carpoolTimedOut {
		return
	}
	if w.ctx.Err() != nil {
		if w.state == carpoolWaiting {
			l.leaveQueueLocked(w)
		}
		l.dropLeaseLocked(w.lease)
		w.lease = carpoolLeaseRef{}
		w.state = carpoolCancelled
		return
	}
	if w.state == carpoolWaiting {
		// Re-read at each handoff: processing other users must not make this
		// decision use a timestamp from before this waiter's deadline.
		now := l.now()
		mode, limit := p.forUser(w.user.id, now)
		if limit > 0 {
			w.limit = limit
		}
		if !now.Before(w.deadline) {
			l.leaveQueueLocked(w)
			w.state = carpoolTimedOut
			*logs = append(*logs, fmt.Sprintf("[carpool-concurrency] reject user=%d inflight=%d limit=%d reason=timeout", w.user.id, w.user.inflight, w.limit))
			return
		}
		switch mode {
		case "off":
			w.state = carpoolReleasedByPolicy
		case "observe":
			l.observeLocked(w.user, limit, w.path, logs)
			w.lease = l.leaseLocked(w.user, now)
			w.state = carpoolReleasedByPolicy
		case "enforce":
			if accept || w.user.inflight >= limit {
				return // Only the FIFO dispatcher may grant a waiting request.
			}
			w.lease = l.leaseLocked(w.user, now)
			w.state = carpoolGranted
		}
		l.leaveQueueLocked(w)
	}
	if accept {
		w.state = carpoolAccepted
	}
}

func (l *carpoolLimiter) applyUserLocked(user *carpoolUserState, p *carpoolPolicy, now time.Time, logs *[]string) {
	for _, lease := range user.leases {
		if !lease.warned && now.Sub(lease.startedAt) > 30*time.Minute {
			lease.warned = true
			*logs = append(*logs, fmt.Sprintf("[carpool-concurrency] long lease user=%d age=%s", user.id, now.Sub(lease.startedAt)))
		}
	}
	for i := 0; i < len(user.waiters); {
		w := user.waiters[i]
		l.decideLocked(w, false, p, logs)
		if w.state == carpoolWaiting {
			i++
		}
	}
	l.cleanupLocked(user)
}

func (l *carpoolLimiter) acquire(ctx context.Context, id int, path string) (a carpoolAdmission) {
	l.RefreshAndApply(func(p *carpoolPolicy, now time.Time, logs *[]string) {
		if ctx.Err() != nil {
			a.cancelled = true
			return
		}
		mode, limit := p.forUser(id, now)
		if mode == "off" {
			return
		}
		user := l.users[id]
		if user == nil {
			user = &carpoolUserState{id: id, leases: make(map[uint64]*carpoolLease)}
			l.users[id] = user
		}
		a.limit = limit
		if mode == "observe" {
			l.observeLocked(user, limit, path, logs)
			a.lease = l.leaseLocked(user, now)
		} else if user.inflight < limit && len(user.waiters) == 0 {
			a.lease = l.leaseLocked(user, now)
		} else if len(user.waiters) >= limit {
			a.rejected = true
			*logs = append(*logs, fmt.Sprintf("[carpool-concurrency] reject user=%d inflight=%d limit=%d reason=queue_full", id, user.inflight, limit))
		} else {
			a.waiter = &carpoolWaiter{user: user, ctx: ctx, path: path, deadline: now.Add(p.wait), notify: make(chan struct{}), limit: limit}
			user.waiters = append(user.waiters, a.waiter)
			l.applyUserLocked(user, p, now, logs)
		}
	})
	return a
}

func (l *carpoolLimiter) resolve(w *carpoolWaiter) (state carpoolWaiterState, lease carpoolLeaseRef, limit int) {
	l.RefreshAndApply(func(p *carpoolPolicy, now time.Time, logs *[]string) {
		l.decideLocked(w, true, p, logs)
		l.applyUserLocked(w.user, p, now, logs)
		state, lease, limit = w.state, w.lease, w.limit
	})
	return
}

func (l *carpoolLimiter) release(ref carpoolLeaseRef) {
	if ref.id == 0 {
		return
	}
	l.RefreshAndApply(func(p *carpoolPolicy, now time.Time, logs *[]string) {
		l.dropLeaseLocked(ref)
		l.applyUserLocked(ref.user, p, now, logs)
	})
}

var sharedCarpoolLimiter = newCarpoolLimiter()

// CarpoolConcurrencyLimit shares one process-local limiter across relay groups.
// It must run after TokenAuth and before ModelRequestRateLimit.
func CarpoolConcurrencyLimit() gin.HandlerFunc {
	return sharedCarpoolLimiter.middleware()
}

func (l *carpoolLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		a := l.acquire(c.Request.Context(), c.GetInt("id"), c.Request.URL.Path)
		if a.waiter != nil {
			w := a.waiter
			timer := time.NewTimer(max(0, w.deadline.Sub(l.now())))
			defer timer.Stop()
			ticker := time.NewTicker(l.checkInterval)
			defer ticker.Stop()
			for {
				select {
				case <-w.notify:
				case <-timer.C:
				case <-w.ctx.Done():
				case <-ticker.C:
				}
				state, lease, limit := l.resolve(w)
				if state == carpoolWaiting {
					continue
				}
				a.lease, a.limit = lease, limit
				a.cancelled = state == carpoolCancelled
				a.rejected = state == carpoolTimedOut
				break
			}
		}
		if a.cancelled {
			c.Abort()
			return
		}
		if a.rejected {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("当前套餐最多同时进行 %d 个请求，请稍后重试", a.limit), types.ErrorCode("carpool_concurrency_limit"))
			return
		}
		if a.lease.id != 0 {
			defer l.release(a.lease)
		}
		c.Next()
	}
}
