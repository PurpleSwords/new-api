package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func AlphaSearchHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	switch info.ChannelType {
	case constant.ChannelTypeSub2API,
		constant.ChannelTypeNewAPI,
		constant.ChannelTypeCodex,
		constant.ChannelTypeAdvancedCustom,
		constant.ChannelTypeOpenAI:
	default:
		// Allow retry onto another channel that may support this endpoint.
		return types.NewError(
			errors.New("channel does not support /v1/alpha/search"),
			types.ErrorCodeInvalidRequest,
		)
	}

	request, ok := info.Request.(*dto.AlphaSearchRequest)
	if !ok {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("invalid request type, expected *dto.AlphaSearchRequest, got %T", info.Request),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	err := helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	isOpenAIResponsesSearch := info.ChannelType == constant.ChannelTypeOpenAI
	var jsonData []byte
	if isOpenAIResponsesSearch {
		originalIsStream := info.IsStream
		info.IsStream = true
		defer func() { info.IsStream = originalIsStream }()

		jsonData, err = buildOpenAIResponsesAlphaSearchBody(c, request, info, adaptor)
	} else {
		jsonData, err = buildAlphaSearchRequestBody(request.RawBody, info.OriginModelName, info.UpstreamModelName)
	}
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}

	if len(info.ParamOverride) > 0 {
		jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
		if err != nil {
			return newAPIErrorFromParamOverride(err)
		}
	}

	logger.LogDebug(c, "requestBody: %s", jsonData)
	body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
	if err != nil {
		return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
	}
	defer closer.Close()

	resp, err := adaptor.DoRequest(c, info, body)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		return types.NewOpenAIError(errors.New("invalid http response"), types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}
	if isOpenAIResponsesSearch {
		usage, responseErr := writeOpenAIResponsesAlphaSearchResult(c, info, httpResp)
		if responseErr != nil {
			return responseErr
		}
		service.PostTextConsumeQuota(c, info, usage, nil)
		return nil
	}

	if contentType := httpResp.Header.Get("Content-Type"); contentType != "" {
		c.Writer.Header().Set("Content-Type", contentType)
	}
	c.Writer.WriteHeader(httpResp.StatusCode)
	if _, err := io.Copy(c.Writer, httpResp.Body); err != nil {
		return types.NewError(err, types.ErrorCodeDoRequestFailed, types.ErrOptionWithSkipRetry())
	}

	// Upstream alpha search returns no usage; bill one web_search_preview call.
	if info.ResponsesUsageInfo == nil {
		info.ResponsesUsageInfo = &relaycommon.ResponsesUsageInfo{
			BuiltInTools: make(map[string]*relaycommon.BuildInToolInfo),
		}
	}
	if info.ResponsesUsageInfo.BuiltInTools == nil {
		info.ResponsesUsageInfo.BuiltInTools = make(map[string]*relaycommon.BuildInToolInfo)
	}
	info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearchPreview] = &relaycommon.BuildInToolInfo{
		ToolName:  dto.BuildInToolWebSearchPreview,
		CallCount: 1,
	}

	usage := &dto.Usage{}
	service.PostTextConsumeQuota(c, info, usage, nil)
	return nil
}

// buildAlphaSearchRequestBody returns RawBody unchanged unless the model was
// mapped, in which case only the "model" field is rewritten so unknown fields
// are preserved.
func buildAlphaSearchRequestBody(rawBody []byte, originModel, upstreamModel string) ([]byte, error) {
	if len(rawBody) == 0 {
		return nil, errors.New("empty alpha search request body")
	}
	if upstreamModel == "" || upstreamModel == originModel {
		return rawBody, nil
	}
	var body map[string]any
	if err := common.Unmarshal(rawBody, &body); err != nil {
		return nil, err
	}
	body["model"] = upstreamModel
	return common.Marshal(body)
}

type alphaSearchForwardRequest struct {
	Input    json.RawMessage `json:"input,omitempty"`
	Commands json.RawMessage `json:"commands,omitempty"`
}

func buildOpenAIResponsesAlphaSearchBody(c *gin.Context, request *dto.AlphaSearchRequest, info *relaycommon.RelayInfo, adaptor interface {
	ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error)
}) ([]byte, error) {
	if request == nil {
		return nil, errors.New("alpha search request is nil")
	}

	var forwardRequest alphaSearchForwardRequest
	if err := common.Unmarshal(request.RawBody, &forwardRequest); err != nil {
		return nil, err
	}

	input, err := common.Marshal([]map[string]any{
		{
			"type": "message",
			"role": "user",
			"content": []map[string]string{
				{"type": "input_text", "text": buildOpenAIAlphaSearchPrompt(forwardRequest)},
			},
		},
	})
	if err != nil {
		return nil, err
	}
	tools, err := common.Marshal([]map[string]any{
		{
			"type":                 dto.BuildInToolWebSearch,
			"external_web_access":  true,
			"search_content_types": []string{"text", "image"},
		},
	})
	if err != nil {
		return nil, err
	}

	modelName := info.UpstreamModelName
	if modelName == "" {
		modelName = request.Model
	}
	stream := true
	responsesRequest := dto.OpenAIResponsesRequest{
		Model:           modelName,
		Input:           input,
		MaxOutputTokens: request.MaxOutputTokens,
		Store:           json.RawMessage("false"),
		Stream:          &stream,
		Tools:           tools,
	}
	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, responsesRequest)
	if err != nil {
		return nil, err
	}
	relaycommon.AppendRequestConversionFromRequest(info, converted)
	return common.Marshal(converted)
}

func buildOpenAIAlphaSearchPrompt(request alphaSearchForwardRequest) string {
	queries := extractAlphaSearchQueries(request.Commands)
	if inputText := extractAlphaSearchInput(request.Input); inputText != "" {
		queries = append(queries, inputText)
	}
	if len(queries) == 0 {
		queries = append(queries, "the requested topic")
	}
	return "Please search the web for: " + strings.Join(queries, "; ") +
		". Summarize the results and return only the final answer with source links."
}

func extractAlphaSearchQueries(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var commands struct {
		SearchQuery []struct {
			Query   string   `json:"q"`
			Domains []string `json:"domains"`
		} `json:"search_query"`
	}
	if err := common.Unmarshal(raw, &commands); err != nil {
		return nil
	}
	queries := make([]string, 0, len(commands.SearchQuery))
	for _, item := range commands.SearchQuery {
		query := strings.TrimSpace(item.Query)
		if query == "" {
			continue
		}
		if len(item.Domains) > 0 {
			query += " (prefer sources from " + strings.Join(item.Domains, ", ") + ")"
		}
		queries = append(queries, query)
	}
	return queries
}

func extractAlphaSearchInput(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var textValue string
	if err := common.Unmarshal(raw, &textValue); err != nil {
		return ""
	}
	return strings.TrimSpace(textValue)
}

type openAIResponsesAlphaSearchResult struct {
	Output string
	Usage  dto.Usage
}

func writeOpenAIResponsesAlphaSearchResult(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(c, "OpenAI alpha search upstream response: content_type=%q body_bytes=%d", resp.Header.Get("Content-Type"), len(responseBody))

	result, parseErr := parseOpenAIResponsesAlphaSearchOutput(responseBody, resp.Header.Get("Content-Type"))
	if parseErr != nil {
		return nil, parseErr
	}
	normalizeOpenAIResponsesAlphaSearchUsage(info, &result)

	searchResponse, err := common.Marshal(struct {
		Output string `json:"output"`
	}{Output: result.Output})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Del("Cache-Control")
	c.Writer.Header().Del("Connection")
	c.Writer.Header().Del("Transfer-Encoding")
	c.Writer.Header().Del("X-Accel-Buffering")
	c.Data(resp.StatusCode, "application/json", searchResponse)
	return &result.Usage, nil
}

func parseOpenAIResponsesAlphaSearchOutput(responseBody []byte, contentType string) (openAIResponsesAlphaSearchResult, *types.NewAPIError) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || looksLikeSSEData(responseBody) {
		return parseOpenAIResponsesAlphaSearchStream(responseBody)
	}

	var responsesResponse dto.OpenAIResponsesResponse
	if err := common.Unmarshal(responseBody, &responsesResponse); err != nil {
		return openAIResponsesAlphaSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return openAIResponsesAlphaSearchResult{}, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
	}
	return openAIResponsesAlphaSearchResult{
		Output: extractOpenAIResponsesAlphaSearchText(responsesResponse),
		Usage:  openAIResponsesAlphaSearchUsage(responsesResponse.Usage),
	}, nil
}

func parseOpenAIResponsesAlphaSearchStream(responseBody []byte) (openAIResponsesAlphaSearchResult, *types.NewAPIError) {
	var output strings.Builder
	var usage dto.Usage
	scanner := bufio.NewScanner(strings.NewReader(string(responseBody)))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}

		var streamResponse dto.ResponsesStreamResponse
		if err := common.Unmarshal([]byte(data), &streamResponse); err != nil {
			return openAIResponsesAlphaSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		switch streamResponse.Type {
		case "response.output_text.delta":
			output.WriteString(streamResponse.Delta)
		case "response.completed", "response.done":
			if streamResponse.Response == nil {
				continue
			}
			if oaiError := streamResponse.Response.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				return openAIResponsesAlphaSearchResult{}, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
			}
			usage = openAIResponsesAlphaSearchUsage(streamResponse.Response.Usage)
			if output.Len() == 0 {
				output.WriteString(extractOpenAIResponsesAlphaSearchText(*streamResponse.Response))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return openAIResponsesAlphaSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	return openAIResponsesAlphaSearchResult{Output: output.String(), Usage: usage}, nil
}

func openAIResponsesAlphaSearchUsage(usage *dto.Usage) dto.Usage {
	if usage == nil {
		return dto.Usage{}
	}
	promptTokens := usage.InputTokens
	if promptTokens == 0 {
		promptTokens = usage.PromptTokens
	}
	completionTokens := usage.OutputTokens
	if completionTokens == 0 {
		completionTokens = usage.CompletionTokens
	}
	result := dto.Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.InputTokensDetails != nil {
		result.PromptTokensDetails = *usage.InputTokensDetails
	} else {
		result.PromptTokensDetails = usage.PromptTokensDetails
	}
	return result
}

func normalizeOpenAIResponsesAlphaSearchUsage(info *relaycommon.RelayInfo, result *openAIResponsesAlphaSearchResult) {
	if result == nil {
		return
	}
	if result.Usage.CompletionTokens == 0 && result.Output != "" {
		modelName := ""
		if info != nil {
			modelName = info.UpstreamModelName
		}
		result.Usage.CompletionTokens = service.CountTextToken(result.Output, modelName)
	}
	if info != nil && result.Usage.PromptTokens == 0 && result.Usage.CompletionTokens != 0 {
		result.Usage.PromptTokens = info.GetEstimatePromptTokens()
	}
	result.Usage.TotalTokens = result.Usage.PromptTokens + result.Usage.CompletionTokens
}

func extractOpenAIResponsesAlphaSearchText(response dto.OpenAIResponsesResponse) string {
	var output strings.Builder
	for _, item := range response.Output {
		for _, content := range item.Content {
			if content.Text == "" {
				continue
			}
			if output.Len() > 0 {
				output.WriteString("\n")
			}
			output.WriteString(content.Text)
		}
	}
	return output.String()
}

func looksLikeSSEData(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	return strings.HasPrefix(trimmed, "data:") || strings.Contains(trimmed, "\ndata:")
}
