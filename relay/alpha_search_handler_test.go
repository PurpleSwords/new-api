package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type alphaSearchResponsesConverter struct{}

func (alphaSearchResponsesConverter) ConvertOpenAIResponsesRequest(_ *gin.Context, _ *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	return request, nil
}

func TestBuildAlphaSearchRequestBodyPreservesUnknownFields(t *testing.T) {
	raw := []byte(`{
		"id":"req_1",
		"model":"gpt-5.1",
		"input":[{"role":"user","content":"hi"}],
		"commands":{"search_query":[{"q":"weather","recency":1}]},
		"settings":{"locale":"en"},
		"future_field":{"nested":true}
	}`)

	out, err := buildAlphaSearchRequestBody(raw, "gpt-5.1", "gpt-5.1-mapped")
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, common.Unmarshal(out, &body))
	assert.Equal(t, "gpt-5.1-mapped", body["model"])
	assert.Equal(t, "req_1", body["id"])
	require.Contains(t, body, "commands")
	require.Contains(t, body, "settings")
	require.Contains(t, body, "future_field")
	require.Contains(t, body, "input")

	commands, ok := body["commands"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, commands, "search_query")

	future, ok := body["future_field"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, future["nested"])
}

func TestBuildAlphaSearchRequestBodyNoMappingKeepsRawBytes(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.1","commands":{"search_query":[{"q":"x"}]},"future_field":1}`)
	out, err := buildAlphaSearchRequestBody(raw, "gpt-5.1", "gpt-5.1")
	require.NoError(t, err)
	assert.Equal(t, raw, out)
}

func TestBuildOpenAIResponsesAlphaSearchBody(t *testing.T) {
	maxOutputTokens := uint(4096)
	request := &dto.AlphaSearchRequest{
		Model:           "gpt-5.4",
		MaxOutputTokens: &maxOutputTokens,
		RawBody: []byte(`{
			"model":"gpt-5.4",
			"input":"include current context",
			"commands":{"search_query":[{"q":"latest release","domains":["example.com"]}]}
		}`),
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.4-mini"},
		RelayFormat: types.RelayFormatOpenAIAlphaSearch,
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: {CallCount: 0},
			},
		},
	}

	body, err := buildOpenAIResponsesAlphaSearchBody(nil, request, info, alphaSearchResponsesConverter{})
	require.NoError(t, err)

	var responsesRequest dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(body, &responsesRequest))
	assert.Equal(t, "gpt-5.4-mini", responsesRequest.Model)
	require.NotNil(t, responsesRequest.Stream)
	assert.True(t, *responsesRequest.Stream)
	assert.Equal(t, "false", string(responsesRequest.Store))
	require.NotNil(t, responsesRequest.MaxOutputTokens)
	assert.EqualValues(t, 4096, *responsesRequest.MaxOutputTokens)

	var input []map[string]any
	require.NoError(t, common.Unmarshal(responsesRequest.Input, &input))
	require.Len(t, input, 1)
	content, ok := input[0]["content"].([]any)
	require.True(t, ok)
	require.Len(t, content, 1)
	contentItem, ok := content[0].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, contentItem["text"], "latest release (prefer sources from example.com)")
	assert.Contains(t, contentItem["text"], "include current context")

	var tools []map[string]any
	require.NoError(t, common.Unmarshal(responsesRequest.Tools, &tools))
	require.Len(t, tools, 1)
	assert.Equal(t, dto.BuildInToolWebSearch, tools[0]["type"])
	assert.Equal(t, true, tools[0]["external_web_access"])
	assert.Empty(t, responsesRequest.ToolChoice)
	assert.Equal(t, types.RelayFormat(types.RelayFormatOpenAIResponses), info.GetFinalRequestRelayFormat())
	assert.Equal(t, 0, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview].CallCount)
	assert.NotContains(t, info.ResponsesUsageInfo.BuiltInTools, dto.BuildInToolWebSearch)
}

func TestParseOpenAIResponsesAlphaSearchJSONIncludesUsage(t *testing.T) {
	body := []byte(`{
		"status":"completed",
		"output":[{"type":"message","content":[{"type":"output_text","text":"answer with source"}]}],
		"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19,"input_tokens_details":{"cached_tokens":3,"cache_write_tokens":2}}
	}`)

	result, apiErr := parseOpenAIResponsesAlphaSearchOutput(body, "application/json")
	require.Nil(t, apiErr)
	assert.Equal(t, "answer with source", result.Output)
	assert.Equal(t, 12, result.Usage.PromptTokens)
	assert.Equal(t, 7, result.Usage.CompletionTokens)
	assert.Equal(t, 19, result.Usage.TotalTokens)
	assert.Equal(t, 3, result.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 2, result.Usage.PromptTokensDetails.CacheWriteTokens)
}

func TestParseOpenAIResponsesAlphaSearchStreamIncludesUsage(t *testing.T) {
	body := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"answer \"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"with source\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":8,\"output_tokens\":4,\"total_tokens\":12}}}\n\n" +
		"data: [DONE]\n\n")

	result, apiErr := parseOpenAIResponsesAlphaSearchOutput(body, "text/event-stream")
	require.Nil(t, apiErr)
	assert.Equal(t, "answer with source", result.Output)
	assert.Equal(t, 8, result.Usage.PromptTokens)
	assert.Equal(t, 4, result.Usage.CompletionTokens)
	assert.Equal(t, 12, result.Usage.TotalTokens)
}

func TestNormalizeOpenAIResponsesAlphaSearchUsageFallback(t *testing.T) {
	service.InitTokenEncoders()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.4"}}
	info.SetEstimatePromptTokens(13)
	result := openAIResponsesAlphaSearchResult{Output: "answer"}

	normalizeOpenAIResponsesAlphaSearchUsage(info, &result)

	assert.Equal(t, 13, result.Usage.PromptTokens)
	assert.Greater(t, result.Usage.CompletionTokens, 0)
	assert.Equal(t, result.Usage.PromptTokens+result.Usage.CompletionTokens, result.Usage.TotalTokens)
}
