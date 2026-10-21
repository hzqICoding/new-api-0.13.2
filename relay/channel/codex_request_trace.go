package channel

import (
	"net/http"
	"os"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const codexTraceHeader = "X-Newapi-Request-ID"

// This is opt-in metadata for the locally operated Codex gateway. It never reads
// the request body or logs credentials, URL queries, prompts or response text.
func traceCodexRequest(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("CODEX_RELAY_TRACE_ENABLED")), "true") ||
		c == nil || req == nil || req.URL == nil || info == nil || info.ChannelMeta == nil {
		return
	}
	if req.Method != http.MethodPost || req.URL.Scheme != "http" ||
		!strings.EqualFold(req.URL.Hostname(), "codex2api") || req.URL.Port() != "8080" || req.URL.User != nil {
		return
	}
	switch req.URL.Path {
	case "/v1/responses", "/v1/responses/compact", "/v1/chat/completions", "/v1/completions", "/v1/messages":
	default:
		return
	}
	// Apply this after channel header overrides: a client must not impersonate a
	// server-generated correlation ID through header passthrough.
	req.Header.Del(codexTraceHeader)
	id := c.GetString(common.RequestIdKey)
	if id == "" {
		id = info.RequestId
	}
	if !validCodexTraceID(id) {
		return
	}
	req.Header.Set(codexTraceHeader, id)
	bytes := req.ContentLength
	known := bytes >= 0
	if req.Body != nil && req.Body != http.NoBody && bytes == 0 {
		// net/http uses 0 for an unknown length on a non-empty reader as well.
		bytes, known = -1, false
	}
	if !known {
		bytes = -1
	}
	event := struct {
		RequestID   string `json:"request_id"`
		UserID      int    `json:"user_id"`
		ChannelID   int    `json:"channel_id"`
		Attempt     int    `json:"attempt"`
		Path        string `json:"path"`
		BodyBytes   int64  `json:"body_bytes"`
		BytesKnown  bool   `json:"body_bytes_known"`
		Stream      bool   `json:"stream"`
		ChannelTest bool   `json:"channel_test"`
	}{id, info.UserId, info.ChannelId, info.RetryIndex, req.URL.Path, bytes, known, info.IsStream, info.IsChannelTest}
	encoded, err := common.Marshal(event)
	if err == nil {
		logger.LogInfo(c, "codex_relay_request "+string(encoded))
	}
}

func validCodexTraceID(id string) bool {
	if len(id) == 0 || len(id) > 80 {
		return false
	}
	for _, ch := range id {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == '_' {
			continue
		}
		return false
	}
	return true
}
