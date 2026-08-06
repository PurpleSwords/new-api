package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay/channel/openai"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestBuildOpenAIResponsesSearchBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-5.6-luna",
		},
	}

	body, err := buildOpenAIResponsesSearchBody([]byte(`{
		"model":"gpt-5.6-luna",
		"commands":{"search_query":[{"q":"Google latest model"}]},
		"settings":{"external_web_access":true}
	}`), info, &openai.Adaptor{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var request map[string]any
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request["model"] != "gpt-5.6-luna" {
		t.Fatalf("unexpected model: %v", request["model"])
	}
	if request["store"] != false || request["stream"] != true {
		t.Fatalf("expected streaming non-stored request: %#v", request)
	}
	input, ok := request["input"].([]any)
	if !ok || len(input) != 1 {
		t.Fatalf("unexpected input: %#v", request["input"])
	}
	if !strings.Contains(input[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string), "Google latest model") {
		t.Fatalf("search query was not included in input: %v", request["input"])
	}
	prompt := input[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if strings.Contains(prompt, `"search_query"`) {
		t.Fatalf("search command JSON should be converted to natural language: %s", prompt)
	}
	if _, exists := request["tool_choice"]; exists {
		t.Fatalf("did not expect tool_choice override: %#v", request["tool_choice"])
	}
	tools, ok := request["tools"].([]any)
	if !ok || len(tools) != 1 || tools[0].(map[string]any)["type"] != "web_search" {
		t.Fatalf("unexpected tools: %#v", request["tools"])
	}
}

func TestParseOpenAIResponsesSearchJSONIncludesUsage(t *testing.T) {
	result, apiErr := parseOpenAIResponsesSearchOutput([]byte(`{
		"output":[{"type":"message","content":[{"type":"output_text","text":"latest news"}]}],
		"usage":{
			"input_tokens":120,
			"output_tokens":30,
			"total_tokens":150,
			"input_tokens_details":{"cached_tokens":40,"cache_write_tokens":8}
		}
	}`), "application/json")
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if result.Output != "latest news" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if result.Usage.PromptTokens != 120 || result.Usage.CompletionTokens != 30 || result.Usage.TotalTokens != 150 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	if result.Usage.PromptTokensDetails.CachedTokens != 40 || result.Usage.PromptTokensDetails.CacheWriteTokens != 8 {
		t.Fatalf("unexpected input token details: %#v", result.Usage.PromptTokensDetails)
	}
}

func TestParseOpenAIResponsesSearchStreamIncludesUsage(t *testing.T) {
	body := strings.Join([]string{
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"latest "}`,
		``,
		`event: response.output_text.delta`,
		`data: {"type":"response.output_text.delta","delta":"news"}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":90,"output_tokens":10,"total_tokens":100,"input_tokens_details":{"cached_tokens":32}}}}`,
		``,
	}, "\n")

	result, apiErr := parseOpenAIResponsesSearchOutput([]byte(body), "text/event-stream")
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if result.Output != "latest news" {
		t.Fatalf("unexpected output: %q", result.Output)
	}
	if result.Usage.PromptTokens != 90 || result.Usage.CompletionTokens != 10 || result.Usage.TotalTokens != 100 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
	if result.Usage.PromptTokensDetails.CachedTokens != 32 {
		t.Fatalf("unexpected cached tokens: %#v", result.Usage.PromptTokensDetails)
	}
}

func TestParseOpenAIResponsesSearchUsageWithoutDetails(t *testing.T) {
	result, apiErr := parseOpenAIResponsesSearchOutput([]byte(`{
		"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}],
		"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}
	}`), "application/json")
	if apiErr != nil {
		t.Fatal(apiErr)
	}
	if result.Usage.PromptTokens != 2 || result.Usage.CompletionTokens != 1 {
		t.Fatalf("unexpected usage: %#v", result.Usage)
	}
}
