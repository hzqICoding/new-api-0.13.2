package middleware

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

func carpoolTestRouter(handler gin.HandlerFunc, downstream gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 741)
		c.Set(common.RequestIdKey, "carpool-test-request")
	})
	r.Use(handler)
	r.POST("/relay", downstream)
	return r
}

func carpoolStartRequest(r http.Handler, ctx context.Context) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/relay", nil).WithContext(ctx))
		done <- w
	}()
	return done
}

func carpoolResponse(t *testing.T, done <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-done:
		return response
	case <-time.After(time.Second):
		t.Fatal("request did not finish within 1 second")
		return nil
	}
}

func carpoolAssert429(t *testing.T, w *httptest.ResponseRecorder, wantMessage string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusTooManyRequests || body.Error.Code != "carpool_concurrency_limit" || body.Error.Type != "new_api_error" || body.Error.Message != wantMessage+" (request id: carpool-test-request)" {
		t.Fatalf("unexpected rejection: %d %s", w.Code, w.Body.String())
	}
}

func TestCarpoolMiddlewareBypass(t *testing.T) {
	for _, mode := range []string{"missing", "off", "unlisted", "expired", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			h := newCarpoolTestRig(t)
			switch mode {
			case "off":
				h.set(t, "off", 1, 10)
			case "unlisted":
				p := h.policy("enforce", 1, 10)
				p["user_limits"] = map[string]int{"742": 1}
				h.raw.Store(carpoolJSON(t, p))
			case "expired":
				h.set(t, "enforce", 1, 10)
				h.advance(2 * time.Hour)
			case "invalid":
				h.raw.Store("invalid")
			}
			r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) {
				h.assertCounts(t, 0, 0, 0)
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/relay", nil))
			if w.Code != http.StatusNoContent {
				t.Fatal("bypass did not execute downstream")
			}
			h.assertCounts(t, 0, 0, 0)
		})
	}
}

func TestCarpoolMiddlewareObserve(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "observe", 1, 10)
	holder := h.l.acquire(context.Background(), 741, "/held")
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) {
		h.assertCounts(t, 2, 0, 1)
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/relay?key=secret-query", strings.NewReader("secret-body"))
	req.Header.Set("Authorization", "Bearer secret-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent || h.logCount("observe over-limit user=741 inflight=1 limit=1 path=/relay") != 1 || h.logCount("secret") != 0 {
		t.Fatal("observe did not pass through or log safe fields")
	}
	h.l.release(holder.lease)
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolMiddlewareQueueAndReject(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 3)
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	var calls atomic.Int32
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) {
		calls.Add(1) // Stands at the frequency limiter's position in the chain.
		entered <- struct{}{}
		<-release
		c.Status(http.StatusNoContent)
	})
	first := carpoolStartRequest(r, context.Background())
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("direct acquisition did not enter")
	}
	second := carpoolStartRequest(r, context.Background())
	carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
	third := carpoolStartRequest(r, context.Background())
	carpoolAssert429(t, carpoolResponse(t, third), "当前座位最大并发为 1，排队已满，请稍后重试")
	if calls.Load() != 1 || h.logCount("reason=queue_full") != 1 {
		t.Fatal("queued or rejected request consumed downstream work")
	}
	unblock()
	if carpoolResponse(t, first).Code != http.StatusNoContent || carpoolResponse(t, second).Code != http.StatusNoContent || calls.Load() != 2 {
		t.Fatal("queued request did not run after release")
	}
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolMiddlewareTimeout(t *testing.T) {
	for _, wait := range []int{0, 3, 10} {
		t.Run(time.Duration(wait).String(), func(t *testing.T) {
			h := newCarpoolTestRig(t)
			h.set(t, "enforce", 1, wait)
			holder := h.l.acquire(context.Background(), 741, "/hold")
			defer h.l.release(holder.lease)
			var called atomic.Bool
			r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) { called.Store(true) })
			done := carpoolStartRequest(r, context.Background())
			if wait > 0 {
				carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
				h.advance(time.Duration(wait) * time.Second)
			}
			carpoolAssert429(t, carpoolResponse(t, done), fmt.Sprintf("当前座位最大并发为 1，已等待 %d 秒仍无空位，请稍后重试", wait))
			if called.Load() || h.logCount("reason=timeout") != 1 {
				t.Fatal("timeout did not abort downstream exactly once")
			}
			h.assertCounts(t, 1, 0, 1)
		})
	}
}

func TestCarpoolMiddlewareTimeoutUsesEnqueuedWaitAndDecisionLimit(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 3, 3)
	for i := 0; i < 3; i++ {
		holder := h.l.acquire(context.Background(), 741, "/hold")
		defer h.l.release(holder.lease)
	}
	var called atomic.Bool
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) { called.Store(true) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := carpoolStartRequest(r, ctx)
	carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
	// The limit follows the decision's policy, but wait stays at the enqueue
	// policy's 3 seconds even when the new wait or elapsed time differs.
	h.set(t, "enforce", 2, 60)
	h.advance(5 * time.Second)
	carpoolAssert429(t, carpoolResponse(t, done), "当前座位最大并发为 2，已等待 3 秒仍无空位，请稍后重试")
	if called.Load() || h.logCount("reject user=741 inflight=3 limit=2 reason=timeout") != 1 {
		t.Fatal("timeout did not preserve the decision limit or abort downstream exactly once")
	}
	h.assertCounts(t, 3, 0, 1)
}

func TestCarpoolMiddlewareCancelAborts(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	holder := h.l.acquire(context.Background(), 741, "/hold")
	defer h.l.release(holder.lease)
	var called atomic.Bool
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) { called.Store(true) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := carpoolStartRequest(r, ctx)
	carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
	cancel()
	w := carpoolResponse(t, done)
	if called.Load() {
		t.Fatal("cancelled request executed downstream: missing Abort")
	}
	if w.Body.Len() != 0 || w.Code != http.StatusOK {
		t.Fatal("cancellation wrote a response")
	}
	h.assertCounts(t, 1, 0, 1)
}

func TestCarpoolCancellationDoesNotReleaseRunningHandler(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) { close(entered); <-release })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := carpoolStartRequest(r, ctx)
	<-entered
	cancel()
	h.l.RefreshAndApply(nil)
	h.assertCounts(t, 1, 0, 1)
	w := h.l.acquire(context.Background(), 741, "/next").waiter
	if w == nil {
		t.Fatal("cancel released a lease before the handler returned")
	}
	unblock()
	carpoolResponse(t, done)
	state, lease, _ := h.l.resolve(w)
	if state != carpoolAccepted {
		t.Fatal("handler return did not release its lease")
	}
	h.l.release(lease)
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolMiddlewarePanicReleases(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.RecoveryWithWriter(io.Discard))
	r.Use(func(c *gin.Context) { c.Set("id", 741) }, h.l.middleware())
	r.POST("/relay", func(c *gin.Context) { panic("test panic") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/relay", nil))
	if w.Code != http.StatusInternalServerError {
		t.Fatal("recovery did not catch the panic")
	}
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolQueuedPolicyChanges(t *testing.T) {
	for _, change := range []string{"off", "expired", "removed", "increase", "observe", "invalid", "missing"} {
		t.Run(change, func(t *testing.T) {
			h := newCarpoolTestRig(t)
			h.set(t, "enforce", 1, 10)
			if change == "expired" {
				p := h.policy("enforce", 1, 10)
				p["expires_at"] = h.base.Add(2 * time.Second).Format(time.RFC3339Nano)
				h.raw.Store(carpoolJSON(t, p))
			}
			holder := h.l.acquire(context.Background(), 741, "/hold")
			defer h.l.release(holder.lease)
			var inflight atomic.Int32
			r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) {
				n, _, _ := h.counts()
				inflight.Store(int32(n))
				c.Status(http.StatusNoContent)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := carpoolStartRequest(r, ctx)
			carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
			h.l.mu.Lock()
			waiter := h.l.users[741].waiters[0]
			deadline := waiter.deadline
			h.l.mu.Unlock()
			start := time.Now()
			// Only change the source/clock. No explicit refresh, release, or new
			// request: the queued request's periodic check must do the work.
			switch change {
			case "expired":
				h.advance(2 * time.Second)
			case "removed":
				p := h.policy("enforce", 1, 60)
				p["user_limits"] = map[string]int{}
				h.raw.Store(carpoolJSON(t, p))
			case "increase":
				h.set(t, "enforce", 2, 60)
			case "observe":
				h.set(t, "observe", 1, 60)
			case "invalid":
				h.raw.Store("invalid")
			case "missing":
				h.raw.Store("")
			default:
				h.set(t, "off", 1, 60)
			}
			if carpoolResponse(t, done).Code != http.StatusNoContent || time.Since(start) >= time.Second {
				t.Fatal("queued policy update was not applied within 1 second")
			}
			if !waiter.deadline.Equal(deadline) {
				t.Fatal("policy update extended the waiter's deadline")
			}
			want := int32(1)
			if change == "observe" || change == "increase" {
				want = 2
			}
			if inflight.Load() != want {
				t.Fatalf("updated policy counted %d, want %d", inflight.Load(), want)
			}
			if change == "observe" && h.logCount("observe over-limit") != 1 {
				t.Fatal("transition to observe did not log")
			}
			h.assertCounts(t, 1, 0, 1)
		})
	}
}

func TestCarpoolRepeatedRefreshDoesNotExtendDeadline(t *testing.T) {
	h := newCarpoolTestRig(t)
	h.set(t, "enforce", 1, 10)
	holder := h.l.acquire(context.Background(), 741, "/hold")
	defer h.l.release(holder.lease)
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := carpoolStartRequest(r, ctx)
	carpoolEventually(t, func() bool { _, q, _ := h.counts(); return q == 1 })
	for i := 0; i < 10; i++ {
		h.set(t, "enforce", 1, 60)
		h.advance(time.Second)
		h.l.RefreshAndApply(nil)
	}
	carpoolAssert429(t, carpoolResponse(t, done), "当前座位最大并发为 1，已等待 10 秒仍无空位，请稍后重试")
}

func TestCarpoolFixedEnforceStress(t *testing.T) {
	h := newCarpoolTestRig(t)
	const limit, workers, rounds = 4, 24, 30
	h.set(t, "enforce", limit, 10)
	var active, peak, over atomic.Int32
	r := carpoolTestRouter(h.l.middleware(), func(c *gin.Context) {
		n := active.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		if n > limit {
			over.Add(1)
		}
		h.l.mu.Lock()
		for _, user := range h.l.users {
			if user.inflight > limit || user.inflight != len(user.leases) || len(user.waiters) > limit {
				over.Add(1)
			}
		}
		h.l.mu.Unlock()
		// Briefly block real handlers so requests genuinely overlap.
		time.Sleep(time.Millisecond)
		active.Add(-1)
		c.Status(http.StatusNoContent)
	})
	start := make(chan struct{})
	var wg sync.WaitGroup
	var successful, rejected, unexpected atomic.Int32
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < rounds; j++ {
				w := httptest.NewRecorder()
				r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/relay", nil))
				switch w.Code {
				case http.StatusNoContent:
					successful.Add(1)
				case http.StatusTooManyRequests:
					rejected.Add(1)
				default:
					unexpected.Add(1)
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	if over.Load() != 0 || peak.Load() > limit || peak.Load() < 2 || successful.Load() == 0 || unexpected.Load() != 0 || successful.Load()+rejected.Load() != workers*rounds {
		t.Fatalf("inflight exceeded cap or stress did not exercise contention: over=%d peak=%d success=%d rejected=%d unexpected=%d", over.Load(), peak.Load(), successful.Load(), rejected.Load(), unexpected.Load())
	}
	h.assertCounts(t, 0, 0, 0)
}

func TestCarpoolMountsShareUserCounter(t *testing.T) {
	// Exercise both public factory calls against the real OptionMap reader.
	common.OptionMapRWMutex.Lock()
	old, existed := common.OptionMap[carpoolConcurrencyPolicyKey]
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	now := time.Now()
	common.OptionMap[carpoolConcurrencyPolicyKey] = carpoolJSON(t, map[string]any{
		"version": 1, "issued_at": now.Format(time.RFC3339Nano), "mode": "enforce", "wait_seconds": 0,
		"expires_at": now.Add(time.Minute).Format(time.RFC3339Nano), "user_limits": map[string]int{"741": 1},
	})
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if existed {
			common.OptionMap[carpoolConcurrencyPolicyKey] = old
		} else {
			delete(common.OptionMap, carpoolConcurrencyPolicyKey)
		}
		common.OptionMapRWMutex.Unlock()
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("id", 741)
		c.Set(common.RequestIdKey, "carpool-test-request")
	})
	var nestedStatus int
	r.Group("/v1", CarpoolConcurrencyLimit()).POST("/relay", func(c *gin.Context) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1beta/relay", nil)
		req.Header.Set("Authorization", "Bearer different-key-same-user")
		r.ServeHTTP(w, req)
		nestedStatus = w.Code
		carpoolAssert429(t, w, "当前座位最大并发为 1，已等待 0 秒仍无空位，请稍后重试")
		c.Status(http.StatusNoContent)
	})
	r.Group("/v1beta", CarpoolConcurrencyLimit()).POST("/relay", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/relay", nil))
	if w.Code != http.StatusNoContent || nestedStatus != http.StatusTooManyRequests {
		t.Fatal("factory calls used separate counters")
	}
	sharedCarpoolLimiter.mu.Lock()
	users := len(sharedCarpoolLimiter.users)
	sharedCarpoolLimiter.mu.Unlock()
	if users != 0 {
		t.Fatal("shared limiter did not clean up")
	}
}

func TestCarpoolRelayRouterOrder(t *testing.T) {
	// Source-level integration guard: testing TokenAuth/Distribute end to end
	// would require a database and upstream. Verify the actual two insertions.
	f, err := parser.ParseFile(token.NewFileSet(), "../router/relay-router.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	uses := make(map[string][]string)
	ast.Inspect(f, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Use" {
			return true
		}
		group, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		for _, arg := range call.Args {
			factory, ok := arg.(*ast.CallExpr)
			if !ok {
				continue
			}
			middleware, ok := factory.Fun.(*ast.SelectorExpr)
			if ok {
				uses[group.Name] = append(uses[group.Name], middleware.Sel.Name)
			}
		}
		return true
	})
	count := 0
	for group, names := range uses {
		for i, name := range names {
			if name == "CarpoolConcurrencyLimit" {
				count++
				if (group != "relayV1Router" && group != "relayGeminiRouter") || i == 0 || i+1 >= len(names) || names[i-1] != "TokenAuth" || names[i+1] != "ModelRequestRateLimit" {
					t.Fatalf("wrong route or middleware order: %s %v", group, names)
				}
			}
		}
	}
	if count != 2 {
		t.Fatalf("middleware mounted %d times, want 2", count)
	}
}
