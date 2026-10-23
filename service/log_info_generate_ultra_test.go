package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestTextLogUltraMetadataPreservesRawEffort(t *testing.T) {
	for _, value := range []*bool{nil, common.GetPointer(false), common.GetPointer(true)} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{}, ReasoningEffort: "xhigh", CodexUltra: value}
		other := GenerateTextOtherInfo(c, info, 1, 1, 1, 0, 0, 0, 0)
		if other["reasoning_effort"] != "xhigh" {
			t.Fatal("raw effort overwritten")
		}
		got, exists := other["ultra"]
		if value == nil {
			if exists {
				t.Fatal("unknown metadata must omit ultra")
			}
		} else if !exists || got != *value {
			t.Fatalf("ultra=%v, expected bool %v", got, *value)
		}
	}
}
