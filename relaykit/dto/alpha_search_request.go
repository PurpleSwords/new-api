package dto

import (
	"encoding/json"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
)

// AlphaSearchRequest is the standalone web search request.
// RawBody preserves the original JSON so unknown fields are forwarded intact.
type AlphaSearchRequest struct {
	Model           string          `json:"model"`
	Id              string          `json:"id,omitempty"`
	Stream          *bool           `json:"stream,omitempty"`
	MaxOutputTokens *uint           `json:"max_output_tokens,omitempty"`
	RawBody         json.RawMessage `json:"-"`
}

func (r *AlphaSearchRequest) GetTokenCountMeta() *types.TokenCountMeta {
	combineText := ""
	if len(r.RawBody) > 0 {
		combineText = string(r.RawBody)
	}
	maxTokens := 0
	if r.MaxOutputTokens != nil {
		maxTokens = int(*r.MaxOutputTokens)
	}
	return &types.TokenCountMeta{
		CombineText: combineText,
		TokenType:   types.TokenTypeTokenizer,
		MaxTokens:   maxTokens,
	}
}

func (r *AlphaSearchRequest) IsStream(_ *http.Request) bool {
	return false
}

func (r *AlphaSearchRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}
