package middleware

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

const (
	relayFailureReasonKey   = "relay_failure_reason" // Also set by service.NewBillingSession.
	relayFailureRecordedKey = "relay_failure_recorded"
	relayFailureBodyLimit   = 4 * 1024
	relayFailureUserLimit   = 30
	relayFailureFallback    = "请求失败，未能解析错误信息，请稍后重试"
)

// Embed the original writer so Flush, Hijack, CloseNotify and Pusher retain
// their original behavior. Only failed writes allocate a bounded body prefix.
type relayFailureWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *relayFailureWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	if w.Status() >= http.StatusBadRequest && n > 0 {
		w.capture(data[:n])
	}
	return n, err
}

func (w *relayFailureWriter) WriteString(data string) (int, error) {
	n, err := w.ResponseWriter.WriteString(data)
	if w.Status() >= http.StatusBadRequest && n > 0 {
		// Convert only the bounded prefix, never the entire response string.
		w.capture([]byte(data[:min(n, relayFailureBodyLimit-len(w.body))]))
	}
	return n, err
}

func (w *relayFailureWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *relayFailureWriter) capture(data []byte) {
	n := min(len(data), relayFailureBodyLimit-len(w.body))
	if n == 0 {
		return
	}
	if w.body == nil {
		w.body = make([]byte, 0, relayFailureBodyLimit)
	}
	w.body = append(w.body, data[:n]...)
}

type relayFailureCount struct {
	minute     int64
	count      int
	suppressed int
}

type relayFailureLimiter struct {
	mu    sync.Mutex
	users map[int]relayFailureCount
}

func newRelayFailureLimiter() *relayFailureLimiter {
	return &relayFailureLimiter{users: make(map[int]relayFailureCount)}
}

func (l *relayFailureLimiter) allow(userID int, now time.Time) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.users[userID]
	minute := now.Unix() / 60
	if entry.minute < minute {
		entry.minute, entry.count = minute, 0
	}
	if entry.count >= relayFailureUserLimit {
		entry.suppressed++
		l.users[userID] = entry
		return false, 0
	}
	suppressed := entry.suppressed
	entry.count++
	entry.suppressed = 0
	l.users[userID] = entry
	return true, suppressed
}

func (l *relayFailureLimiter) cleanup(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for id, entry := range l.users {
		// Pending suppressed counts must survive idle minutes until reported.
		if entry.minute < now.Unix()/60 && entry.suppressed == 0 {
			delete(l.users, id)
		}
	}
}

var sharedRelayFailureLimiter = newRelayFailureLimiter()
var relayFailureCleanupOnce sync.Once

// RelayFailureLog must run after TokenAuth and before all relay admission checks.
func RelayFailureLog() gin.HandlerFunc {
	relayFailureCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for now := range ticker.C {
				sharedRelayFailureLimiter.cleanup(now)
			}
		}()
	})
	return relayFailureLog(sharedRelayFailureLimiter, time.Now)
}

func relayFailureLog(limiter *relayFailureLimiter, now func() time.Time) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := now()
		writer := &relayFailureWriter{ResponseWriter: c.Writer}
		c.Writer = writer
		c.Next()

		status, userID := writer.Status(), c.GetInt("id")
		if status < http.StatusBadRequest || userID <= 0 || c.GetBool(relayFailureRecordedKey) {
			return
		}
		// Mark even suppressed requests, so nested mounts cannot count twice.
		c.Set(relayFailureRecordedKey, true)
		allowed, suppressed := limiter.allow(userID, now())
		if !allowed {
			return
		}
		content, code, errorType := relayFailureMessage(writer.body)
		reason := c.GetString(relayFailureReasonKey)
		if reason == "" {
			reason = relayFailureReason(c, status, code, errorType)
		}
		start := common.GetContextKeyTime(c, constant.ContextKeyRequestStartTime)
		if start.IsZero() {
			start = started
		}
		other := map[string]interface{}{"failure": map[string]interface{}{
			"status": status, "reason": reason, "code": code,
			"path": c.Request.URL.Path, "suppressed": suppressed,
		}}
		modelName := c.GetString("original_model")
		if modelName == "" {
			modelName = c.GetString("relay_failure_model")
		}
		model.RecordErrorLog(c, userID, c.GetInt("channel_id"), modelName,
			c.GetString("token_name"), content, c.GetInt("token_id"), max(0, int(now().Sub(start).Seconds())),
			common.GetContextKeyBool(c, constant.ContextKeyIsStream), c.GetString("group"), other)
	}
}

func relayFailureMessage(body []byte) (content, code, errorType string) {
	var response struct {
		Error struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
			Type    string          `json:"type"`
		} `json:"error"`
	}
	if common.Unmarshal(body, &response) != nil {
		return relayFailureFallback, "", ""
	}
	content = response.Error.Message
	if content == "" {
		content = relayFailureFallback
	}
	chars := []rune(content)
	if len(chars) > 500 {
		content = string(chars[:500])
	}
	return content, common.JsonRawMessageToString(response.Error.Code), response.Error.Type
}

func relayFailureReason(c *gin.Context, status int, code, errorType string) string {
	if len(c.GetStringSlice("use_channel")) > 0 || errorType == string(types.ErrorTypeUpstreamError) ||
		errorType == string(types.ErrorTypeOpenAIError) {
		return "upstream_error"
	}
	switch types.ErrorCode(code) {
	case types.ErrorCodeInsufficientUserQuota, types.ErrorCodePreConsumeTokenQuotaFailed:
		return "quota_insufficient"
	case types.ErrorCodeModelNotFound:
		return "model_unavailable"
	case "model_rate_limit":
		return "rate_limit"
	case types.ErrorCodeBadResponseStatusCode, types.ErrorCodeDoRequestFailed, types.ErrorCodeReadResponseBodyFailed,
		types.ErrorCodeBadResponse, types.ErrorCodeBadResponseBody, types.ErrorCodeEmptyResponse, types.ErrorCodeAwsInvokeError:
		return "upstream_error"
	}
	if status >= http.StatusInternalServerError {
		return "upstream_error"
	}
	return "other"
}
