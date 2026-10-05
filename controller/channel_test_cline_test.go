package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestClineChannelTestParameters(t *testing.T) {
	for _, endpoint := range []string{"", string(constant.EndpointTypeOpenAI)} {
		for _, stream := range []bool{false, true} {
			channel := &model.Channel{Id: 55}
			r := buildTestRequest("deepseek/deepseek-v4.1-flash", endpoint, channel, stream)
			applyClineChannelTestParameters(channel, r)
			request := r.(*dto.GeneralOpenAIRequest)
			require.Equal(t, uint(256), *request.MaxTokens)
			require.Equal(t, stream, *request.Stream)
			wire, err := common.Marshal(request)
			require.NoError(t, err)
			require.Contains(t, string(wire), `"reasoning":{"enabled":false}`)
			if stream {
				require.True(t, request.StreamOptions.IncludeUsage)
			}
		}
	}
	for _, id := range []int{54, 56} {
		ch := &model.Channel{Id: id}
		r := buildTestRequest("deepseek/deepseek-v4.1-flash", "", ch, false)
		applyClineChannelTestParameters(ch, r)
		req := r.(*dto.GeneralOpenAIRequest)
		require.Equal(t, uint(16), *req.MaxTokens)
		require.Empty(t, req.Reasoning)
	}
	ch := &model.Channel{Id: 55}
	r := buildTestRequest("gpt-6-sol", "", ch, false)
	applyClineChannelTestParameters(ch, r)
	require.Equal(t, uint(16), *r.(*dto.GeneralOpenAIRequest).MaxTokens)
	response := buildTestRequest("deepseek/deepseek-v4.1-flash", string(constant.EndpointTypeOpenAIResponse), ch, false)
	before, err := common.Marshal(response)
	require.NoError(t, err)
	applyClineChannelTestParameters(ch, response)
	after, err := common.Marshal(response)
	require.NoError(t, err)
	require.Equal(t, before, after)
	applyClineChannelTestParameters(nil, r)
}
