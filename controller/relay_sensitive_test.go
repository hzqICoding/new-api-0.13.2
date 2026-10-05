package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func TestRelaySensitiveWordsResponse(t *testing.T) {
	oldEnabled, oldPrompt, oldWords := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords = oldEnabled, oldPrompt, oldWords
	})
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
	setting.SensitiveWords = []string{"test_sensitive"}

	for _, tc := range []struct {
		name, path, body string
		format           types.RelayFormat
	}{
		{"current", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"TEST_SENSITIVE"}]}`, types.RelayFormatOpenAI},
		{"history", "/v1/chat/completions", `{"model":"gpt-4o","messages":[{"role":"user","content":"test_sensitive"},{"role":"assistant","content":"previous reply"},{"role":"user","content":"hello"}]}`, types.RelayFormatOpenAI},
		{"responses", "/v1/responses", `{"model":"gpt-4o","input":"test_sensitive","stream":true}`, types.RelayFormatOpenAIResponses},
		{"claude", "/v1/messages", `{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"test_sensitive"}]}`, types.RelayFormatClaude},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			Relay(c, tc.format)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			var result struct {
				Error struct {
					Message string `json:"message"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := common.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			want := "请求包含敏感词「test_sensitive」，已被拦截。请删除或替换当前消息及历史对话中的相关内容后重试。如仍有问题，请联系管理员。"
			if !strings.HasPrefix(result.Error.Message, want) {
				t.Fatalf("unexpected message: %s", result.Error.Message)
			}
			if tc.format != types.RelayFormatClaude && result.Error.Code != "sensitive_words_detected" {
				t.Fatalf("unexpected error code: %s", result.Error.Code)
			}
		})
	}
}

func TestRelaySensitiveWordsAllowsCleanRequest(t *testing.T) {
	oldEnabled, oldPrompt, oldWords, oldCount := setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords, constant.CountToken
	t.Cleanup(func() {
		setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled, setting.SensitiveWords, constant.CountToken = oldEnabled, oldPrompt, oldWords, oldCount
	})
	setting.CheckSensitiveEnabled, setting.CheckSensitiveOnPromptEnabled = true, true
	setting.SensitiveWords = []string{"test_sensitive"}
	constant.CountToken = false
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"sensitive-test-unpriced-model","messages":[{"role":"user","content":"hello"}]}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(constant.ContextKeyOriginalModel), "sensitive-test-unpriced-model")
	Relay(c, types.RelayFormatOpenAI)
	// Reaching pricing proves the clean request passed the filter, without calling an upstream or charging a user.
	var result struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := common.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Error.Code != string(types.ErrorCodeModelPriceError) {
		t.Fatalf("clean request did not reach pricing: %s", w.Body.String())
	}
}
