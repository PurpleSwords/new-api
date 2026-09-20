package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestWriteOpenAIResponsesAlphaSearchRejectsUnsuccessfulStream(t *testing.T) {
	for _, tt := range []struct {
		name    string
		body    string
		message string
	}{
		{"failed", `{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"upstream failed"}}}`, "upstream failed"},
		{"error", `{"type":"error","code":"server_error","message":"stream failed"}`, "stream failed"},
		{"response error", `{"type":"response.error","message":"response failed"}`, "response failed"},
		{"failed without details", `{"type":"response.failed"}`, "response.failed"},
		{"incomplete", `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`, "response.incomplete"},
		{"done with failed status", `{"type":"response.done","response":{"status":"failed"}}`, "did not complete successfully"},
		{"completed with error", `{"type":"response.completed","response":{"error":{"message":"completion failed"}}}`, "completion failed"},
		{"completed without response", `{"type":"response.completed"}`, "without a completed response"},
		{"done marker only", "[DONE]", "without a completed response"},
		{"truncated", `{"type":"response.output_text.delta","delta":"unfinished"}`, "without a completed response"},
		{"empty", "", "without a completed response"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader("data: " + tt.body + "\n\n")),
			}
			usage, apiErr := writeOpenAIResponsesAlphaSearchResult(c, nil, resp)
			require.NotNil(t, apiErr)
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.Contains(t, apiErr.Error(), tt.message)
			assert.Nil(t, usage)
			assert.False(t, c.Writer.Written(), "leave the response unwritten for error handling or retry")
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestParseOpenAIResponsesAlphaSearchCompletedStreamWithoutDoneMarker(t *testing.T) {
	for _, event := range []string{"response.completed", "response.done"} {
		t.Run(event, func(t *testing.T) {
			body := []byte("data: {\"type\":\"" + event + "\",\"response\":{\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"answer\"}]}]}}\n\n")
			result, apiErr := parseOpenAIResponsesAlphaSearchOutput(body, "text/event-stream")
			require.Nil(t, apiErr)
			assert.Equal(t, "answer", result.Output)
		})
	}
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
