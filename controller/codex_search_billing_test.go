package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
)

func TestUsesResponsesTokenBilling(t *testing.T) {
	tests := []struct {
		name           string
		relayFormat    types.RelayFormat
		channelType    int
		shouldUseToken bool
	}{
		{name: "OpenAI alpha search", relayFormat: types.RelayFormatCodexSearch, channelType: constant.ChannelTypeOpenAI, shouldUseToken: true},
		{name: "Codex alpha search", relayFormat: types.RelayFormatCodexSearch, channelType: constant.ChannelTypeCodex, shouldUseToken: false},
		{name: "ordinary Responses", relayFormat: types.RelayFormatOpenAIResponses, channelType: constant.ChannelTypeOpenAI, shouldUseToken: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := usesResponsesTokenBilling(test.relayFormat, test.channelType); got != test.shouldUseToken {
				t.Fatalf("usesResponsesTokenBilling() = %v, want %v", got, test.shouldUseToken)
			}
		})
	}
}
