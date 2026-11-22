package relay

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
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
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-5.4"}}
	info.SetEstimatePromptTokens(13)
	result := openAIResponsesAlphaSearchResult{Output: "answer"}

	normalizeOpenAIResponsesAlphaSearchUsage(info, &result)

	assert.Equal(t, 13, result.Usage.PromptTokens)
	assert.Greater(t, result.Usage.CompletionTokens, 0)
	assert.Equal(t, result.Usage.PromptTokens+result.Usage.CompletionTokens, result.Usage.TotalTokens)
}

// A single model is rewritten where it stands. Routing reads the first
// "model" and most upstreams read the last, so a repeated model comes out as
// one member carrying the mapped value.
func TestBuildAlphaSearchRequestBodyRewritesModel(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"single model keeps its place", `{"model":"gpt-5.1","input":[]}`, `{"model":"gpt-5.1-mapped","input":[]}`},
		{"repeated model collapses", `{"model":"gpt-5.1","input":[],"model":"gpt-5.5-pro"}`, `{"input":[],"model":"gpt-5.1-mapped"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := buildAlphaSearchRequestBody([]byte(tt.raw), "gpt-5.1", "gpt-5.1-mapped")
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(out))
		})
	}
}

type recordedSettlement struct {
	settled  []int
	refunded bool
}

func (s *recordedSettlement) Settle(quota int) error {
	s.settled = append(s.settled, quota)
	return nil
}
func (s *recordedSettlement) Refund(*gin.Context)    { s.refunded = true }
func (*recordedSettlement) NeedsRefund() bool        { return false }
func (*recordedSettlement) GetPreConsumedQuota() int { return 0 }
func (*recordedSettlement) Reserve(int) error        { return nil }

func (*recordedSettlement) ReserveBeforeSubmit(int) error { return nil }

// /v1/tools/search and /v1/tools/fetch follow /v1/alpha/search: the raw body
// reaches /v1/tools/* unchanged and the response returns unchanged. With only
// the built-in prices, a response DeepSeek bills (results, page content)
// settles one call at the routing name's request price and no tokens, an
// empty one releases the reservation, an upstream error keeps its status, and
// a channel that cannot relay the endpoint fails retryably without sending
// anything.
func TestDeepSeekToolsHelper(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	batch, logConsume := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() { common.BatchUpdateEnabled, common.LogConsumeEnabled = batch, logConsume })

	const search, fetch = "/v1/tools/search", "/v1/tools/fetch"
	const results = `{"results":[{"title":"DeepSeek","url":"https://deepseek.com/","rerank_score":2.1}]}`
	tests := []struct {
		name, path, body, response string
		channelType, status        int
		settled                    []int
		refunded                   bool
	}{
		{name: "search with results", path: search, body: `{"query":"deepseek","count":2,"future_field":{"nested":true}}`,
			response: results, channelType: constant.ChannelTypeDeepSeek, status: http.StatusOK, settled: []int{375}},
		{name: "search without results", path: search, body: `{"query":"site:none.invalid deepseek"}`,
			response: `{"results":[]}`, channelType: constant.ChannelTypeDeepSeek, status: http.StatusOK, refunded: true},
		{name: "fetch with page content", path: fetch, body: `{"url":"https://example.com/"}`,
			response: `{"title":"Example Domain","content":"# Example Domain"}`, channelType: constant.ChannelTypeDeepSeek, status: http.StatusOK, settled: []int{375}},
		{name: "fetch failure keeps its status", path: fetch, body: `{"url":"https://example.com/missing"}`,
			response:    `{"error":{"message":"failed to fetch URL","type":"not_found_error","param":null,"code":"invalid_request_error"}}`,
			channelType: constant.ChannelTypeDeepSeek, status: http.StatusNotFound},
		{name: "search through a New API channel", path: search, body: `{"query":"deepseek"}`,
			response: results, channelType: constant.ChannelTypeNewAPI, status: http.StatusOK, settled: []int{375}},
		{name: "channel without the endpoint", path: search, body: `{"query":"deepseek"}`, channelType: constant.ChannelTypeOpenAI},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			var gotBody []byte
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.response)
			}))
			t.Cleanup(upstream.Close)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			routingName := map[string]string{search: constant.DeepSeekSearchModel, fetch: constant.DeepSeekFetchModel}[tc.path]
			common.SetContextKey(c, constant.ContextKeyOriginalModel, routingName)
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
			common.SetContextKey(c, constant.ContextKeyUserGroup, "default")
			common.SetContextKey(c, constant.ContextKeyChannelType, tc.channelType)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, upstream.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "sk-test")

			request, err := helper.GetAndValidateRequest(c, types.RelayFormatDeepSeekTools)
			require.NoError(t, err)
			info, err := relaycommon.GenRelayInfo(c, types.RelayFormatDeepSeekTools, request, nil)
			require.NoError(t, err)
			info.BillingRequestInput = &billingexpr.RequestInput{Body: []byte(tc.body)}
			_, err = helper.ModelPriceHelper(c, info, 0, request.GetTokenCountMeta())
			require.NoError(t, err, "the routing name is priced out of the box")
			settlement := &recordedSettlement{}
			info.Billing = settlement

			apiErr := DeepSeekToolsHelper(c, info)
			if tc.status == 0 {
				require.NotNil(t, apiErr)
				assert.False(t, types.IsSkipRetryError(apiErr))
				assert.Empty(t, gotPath)
				return
			}
			assert.Equal(t, tc.path, gotPath)
			assert.Equal(t, tc.body, string(gotBody))
			assert.Equal(t, tc.settled, settlement.settled)
			assert.Equal(t, tc.refunded, settlement.refunded)
			if tc.status != http.StatusOK {
				require.NotNil(t, apiErr)
				assert.Equal(t, tc.status, apiErr.StatusCode)
				return
			}
			require.Nil(t, apiErr)
			assert.Equal(t, tc.status, recorder.Code)
			assert.Equal(t, tc.response, recorder.Body.String())
		})
	}
}
