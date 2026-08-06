package openai

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestShouldInjectWebSearchToolFromAdditionalTools(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-luna",
		Input: json.RawMessage(`[{"type":"message","additional_tools":[{"type":"web_search"}]}]`),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}

	if !shouldInjectWebSearchTool(info, request) {
		t.Fatal("expected web_search injection to be enabled")
	}
	tools := withWebSearchTool(request.Tools)
	if !hasWebSearchTool(tools) {
		t.Fatal("expected injected web_search tool")
	}
}

func TestShouldNotInjectWebSearchToolWithoutAdditionalTools(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Model: "gpt-5.6-luna",
		Input: json.RawMessage(`[{"type":"message","content":[{"type":"input_text","text":"hello"}]}]`),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}

	if shouldInjectWebSearchTool(info, request) {
		t.Fatal("did not expect web_search injection")
	}
}

func TestShouldNotDuplicateExistingWebSearchTool(t *testing.T) {
	request := dto.OpenAIResponsesRequest{
		Tools: json.RawMessage(`[{"type":"web_search"}]`),
		Input: json.RawMessage(`[{"additional_tools":["web_search"]}]`),
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI}}

	if shouldInjectWebSearchTool(info, request) {
		t.Fatal("did not expect duplicate web_search injection")
	}
}
