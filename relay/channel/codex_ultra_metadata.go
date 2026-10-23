package channel

import (
	"net/http"
	"net/url"
	"strings"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const codexUltraHeader = "X-Codex2API-Ultra"

func isLocalCodexURL(u *url.URL) bool {
	return u != nil && u.Scheme == "http" && strings.EqualFold(u.Hostname(), "codex2api") &&
		u.Port() == "8080" && u.User == nil && u.Opaque == ""
}

func isLocalCodexResponsesRequest(req *http.Request) bool {
	return req != nil && req.Method == http.MethodPost && isLocalCodexURL(req.URL) && req.URL.Path == "/v1/responses"
}

// Accept only response metadata from the configured internal gateway, including
// the actual destination after redirects. This is independent of request tracing.
// Unknown, malformed, duplicate and third-party values retain the old log shape.
func captureCodexUltraMetadata(req *http.Request, resp *http.Response, info *relaycommon.RelayInfo) {
	if info == nil {
		return
	}
	info.CodexUltra = nil
	if info.ChannelMeta == nil || resp == nil || resp.StatusCode < 200 || resp.StatusCode >= 300 ||
		!isLocalCodexResponsesRequest(req) || !isLocalCodexResponsesRequest(resp.Request) {
		return
	}
	configured, err := url.Parse(info.ChannelBaseUrl)
	if err != nil || !isLocalCodexURL(configured) {
		return
	}
	// Iterate case-insensitively as custom transports can produce noncanonical
	// Header maps; two differently cased entries are still duplicate values.
	var values []string
	for name, entries := range resp.Header {
		if strings.EqualFold(name, codexUltraHeader) {
			values = append(values, entries...)
		}
	}
	if len(values) != 1 {
		return
	}
	var on bool
	switch values[0] {
	case "true":
		on = true
	case "false":
		on = false
	default:
		return
	}
	info.CodexUltra = &on
}
