package channel

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type traceTransport func(*http.Request) (*http.Response, error)

func (f traceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func traceFixture(t *testing.T) (*gin.Context, *relaycommon.RelayInfo, *bytes.Buffer) {
	t.Helper()
	t.Setenv("CODEX_RELAY_TRACE_ENABLED", "true")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses?secret-query=must-not-log", strings.NewReader("inbound secret body"))
	c.Set(common.RequestIdKey, "server-generated-20261021")
	info := &relaycommon.RelayInfo{UserId: 742, RequestId: "info-fallback", RetryIndex: 2,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 59}}
	var log bytes.Buffer
	common.LogWriterMu.Lock()
	previous := gin.DefaultWriter
	gin.DefaultWriter = &log
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previous
		common.LogWriterMu.Unlock()
	})
	return c, info, &log
}

func TestCodexTraceForwardedAtActualTransportWithoutReadingBody(t *testing.T) {
	c, info, log := traceFixture(t)
	service.InitHttpClient()
	client := service.GetHttpClient()
	previous := client.Transport
	t.Cleanup(func() { client.Transport = previous })
	payload := strings.Repeat("private-prompt-", 1024)
	req, err := http.NewRequest(http.MethodPost, "http://codex2api:8080/v1/responses?secret=must-not-log", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(codexTraceHeader, "client-forged-id")
	req.Header.Set("Authorization", "Bearer private-api-key")
	client.Transport = traceTransport(func(sent *http.Request) (*http.Response, error) {
		if sent.Header.Get(codexTraceHeader) != "server-generated-20261021" {
			t.Fatal("server correlation ID was not applied after header overrides")
		}
		if sent.Header.Get("Authorization") != "Bearer private-api-key" {
			t.Fatal("credentials changed")
		}
		body, err := io.ReadAll(sent.Body)
		if err != nil || string(body) != payload {
			t.Fatal("trace consumed or altered request body")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})
	resp, err := DoRequest(c, req, info)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	event := decodeTraceEvent(t, log.String())
	if event["body_bytes"] != float64(len(payload)) || event["body_bytes_known"] != true ||
		event["user_id"] != float64(742) || event["channel_id"] != float64(59) || event["attempt"] != float64(2) {
		t.Fatalf("unexpected trace metadata: %v", event)
	}
	for _, secret := range []string{"private-prompt", "private-api-key", "must-not-log", "client-forged-id", "inbound secret"} {
		if strings.Contains(log.String(), secret) {
			t.Fatalf("sensitive field leaked: %s", secret)
		}
	}
}

func decodeTraceEvent(t *testing.T, line string) map[string]any {
	t.Helper()
	_, value, ok := strings.Cut(line, "codex_relay_request ")
	if !ok {
		t.Fatal("missing metadata record")
	}
	var event map[string]any
	if err := common.Unmarshal([]byte(strings.TrimSpace(value)), &event); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestCodexTraceUnknownLengthIsExplicitWithoutBuffering(t *testing.T) {
	c, info, log := traceFixture(t)
	req := httptest.NewRequest(http.MethodPost, "http://codex2api:8080/v1/responses", nil)
	req.Body, req.ContentLength = &traceUnreadableBody{}, 0
	traceCodexRequest(c, req, info)
	event := decodeTraceEvent(t, log.String())
	if event["body_bytes"] != float64(-1) || event["body_bytes_known"] != false {
		t.Fatal("unknown bytes were reported as a measured zero")
	}
}

type traceUnreadableBody struct{}

func (*traceUnreadableBody) Read([]byte) (int, error) { panic("metadata must not read the body") }
func (*traceUnreadableBody) Close() error             { return nil }

func TestCodexTraceScopeAndDefaultOff(t *testing.T) {
	for _, url := range []string{"http://codex2api.evil:8080/v1/responses", "https://example.test/v1/responses", "http://codex2api:8080/api/admin/settings", "http://codex2api:9090/v1/responses"} {
		t.Run(url, func(t *testing.T) {
			c, info, log := traceFixture(t)
			req := httptest.NewRequest(http.MethodPost, url, strings.NewReader("body"))
			traceCodexRequest(c, req, info)
			if log.Len() != 0 || req.Header.Get(codexTraceHeader) != "" {
				t.Fatal("trace escaped configured local gateway scope")
			}
		})
	}
	t.Run("default-off", func(t *testing.T) {
		c, info, log := traceFixture(t)
		t.Setenv("CODEX_RELAY_TRACE_ENABLED", "")
		req := httptest.NewRequest(http.MethodPost, "http://codex2api:8080/v1/responses", nil)
		traceCodexRequest(c, req, info)
		if log.Len() != 0 || req.Header.Get(codexTraceHeader) != "" {
			t.Fatal("feature enabled without explicit configuration")
		}
	})
}

func TestCodexTraceRejectsInvalidServerIDAndClearsForgedHeader(t *testing.T) {
	for _, id := range []string{"bad\nline", strings.Repeat("x", 81), "id with space", "bad:colon"} {
		t.Run(id, func(t *testing.T) {
			c, info, log := traceFixture(t)
			c.Set(common.RequestIdKey, id)
			req := httptest.NewRequest(http.MethodPost, "http://codex2api:8080/v1/responses", nil)
			req.Header.Set(codexTraceHeader, "forged")
			traceCodexRequest(c, req, info)
			if log.Len() != 0 || req.Header.Get(codexTraceHeader) != "" {
				t.Fatal("invalid or client-supplied ID was forwarded/logged")
			}
		})
	}
}

func TestCodexTraceChannelTestAndEmptyBody(t *testing.T) {
	c, info, log := traceFixture(t)
	c.Set(common.RequestIdKey, "")
	info.IsChannelTest = true
	req := httptest.NewRequest(http.MethodPost, "http://codex2api:8080/v1/chat/completions", nil)
	traceCodexRequest(c, req, info)
	event := decodeTraceEvent(t, log.String())
	if event["channel_test"] != true || event["body_bytes"] != float64(0) || event["body_bytes_known"] != true || event["request_id"] != "info-fallback" {
		t.Fatal("empty/test request metadata is incorrect")
	}
}
