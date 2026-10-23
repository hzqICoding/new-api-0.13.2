package channel

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func TestCodexUltraMetadataTrustAndStrictValues(t *testing.T) {
	const local = "http://codex2api:8080/v1/responses"
	for _, tc := range []struct {
		name, configured, request, final string
		header                           http.Header
		status                           int
		want                             *bool
	}{
		{"true", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true"}}, 200, boolPtr(true)},
		{"false", "http://codex2api:8080/v1", local, local, http.Header{codexUltraHeader: {"false"}}, 200, boolPtr(false)},
		{"missing", "http://codex2api:8080", local, local, nil, 200, nil},
		{"duplicate", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true", "true"}}, 200, nil},
		{"duplicate-case", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true"}, "x-codex2api-ultra": {"false"}}, 200, nil},
		{"combined", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true, false"}}, 200, nil},
		{"invalid-case", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"TRUE"}}, 200, nil},
		{"invalid-space", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {" true"}}, 200, nil},
		{"third-party-config", "http://example.test", local, local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"third-party-request", "http://codex2api:8080", "http://example.test/v1/responses", local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"redirect-third-party", "http://codex2api:8080", local, "http://example.test/v1/responses", http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"userinfo-config", "http://user@codex2api:8080", local, local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"wrong-port", "http://codex2api:9090", local, local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"https", "https://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"lookalike", "http://codex2api.evil:8080", local, local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"compact", "http://codex2api:8080", "http://codex2api:8080/v1/responses/compact", local, http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"no-final-request", "http://codex2api:8080", local, "", http.Header{codexUltraHeader: {"true"}}, 200, nil},
		{"error-response", "http://codex2api:8080", local, local, http.Header{codexUltraHeader: {"true"}}, 400, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tc.configured}, CodexUltra: boolPtr(true)}
			req := httptest.NewRequest(http.MethodPost, tc.request, nil)
			var final *http.Request
			if tc.final != "" {
				final = httptest.NewRequest(http.MethodPost, tc.final, nil)
			}
			resp := &http.Response{Header: tc.header, StatusCode: tc.status, Request: final, Body: &traceUnreadableBody{}}
			captureCodexUltraMetadata(req, resp, info)
			if (info.CodexUltra == nil) != (tc.want == nil) || tc.want != nil && *info.CodexUltra != *tc.want {
				t.Fatalf("metadata=%v, want=%v", info.CodexUltra, tc.want)
			}
		})
	}
}

func boolPtr(value bool) *bool { return &value }

func TestCodexUltraTransportRetryClearsMetadataAndPreservesBodies(t *testing.T) {
	t.Setenv("CODEX_RELAY_TRACE_ENABLED", "false")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: "http://codex2api:8080"}, ReasoningEffort: "xhigh"}
	service.InitHttpClient()
	client := service.GetHttpClient()
	previous := client.Transport
	t.Cleanup(func() { client.Transport = previous })
	const payload = `{"reasoning":{"effort":"xhigh"},"input":"private fixture"}`
	for attempt, destination := range []string{"http://codex2api:8080", "http://third-party.test", "http://codex2api:8080", "http://codex2api:8080"} {
		info.ChannelBaseUrl = destination
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(payload))
		c.Request.Header.Set(codexUltraHeader, "true")
		req, _ := http.NewRequest(http.MethodPost, destination+"/v1/responses", strings.NewReader(payload))
		req.Header.Set(codexUltraHeader, "true")
		client.Transport = traceTransport(func(sent *http.Request) (*http.Response, error) {
			if info.CodexUltra != nil {
				t.Fatal("old attempt metadata reached transport")
			}
			if sent.Header.Get(codexUltraHeader) != "" {
				t.Fatal("client metadata was forwarded")
			}
			body, err := io.ReadAll(sent.Body)
			if err != nil || string(body) != payload {
				t.Fatal("request body changed")
			}
			if attempt == 3 {
				return nil, errors.New("fixture failure")
			}
			header := make(http.Header)
			if attempt != 2 {
				header.Set(codexUltraHeader, "true")
			}
			return &http.Response{StatusCode: 200, Header: header, Request: sent, Body: io.NopCloser(strings.NewReader(payload))}, nil
		})
		resp, err := DoRequest(c, req, info)
		if attempt == 3 {
			if err == nil || info.CodexUltra != nil {
				t.Fatal("failed attempt retained metadata")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != payload || info.ReasoningEffort != "xhigh" {
			t.Fatal("raw effort or response body changed")
		}
		if attempt == 0 {
			if info.CodexUltra == nil || !*info.CodexUltra {
				t.Fatal("trusted response metadata missing with trace disabled")
			}
		} else if info.CodexUltra != nil {
			t.Fatal("untrusted/missing header retained previous metadata")
		}
	}
}
