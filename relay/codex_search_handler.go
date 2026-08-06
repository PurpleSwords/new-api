package relay

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func CodexSearchHelper(c *gin.Context, info *relaycommon.RelayInfo) *types.NewAPIError {
	info.InitChannelMeta(c)
	if info.ChannelType != appconstant.ChannelTypeCodex && info.ChannelType != appconstant.ChannelTypeOpenAI {
		return types.NewErrorWithStatusCode(
			errors.New("standalone search is only supported by Codex or OpenAI channels"),
			types.ErrorCodeInvalidRequest,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(errors.New("invalid Codex channel adaptor"), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}

	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
	}
	if common.DebugEnabled {
		if originalBody, bodyErr := storage.Bytes(); bodyErr == nil {
			relaycommon.LogRequestTrace(c, info, "incoming", c.Request.Method, c.Request.URL.String(), c.Request.Host, c.Request.Header, originalBody)
		}
	}

	var requestBody io.Reader
	var requestBodyCloser io.Closer
	if info.ChannelType == appconstant.ChannelTypeOpenAI {
		originalIsStream := info.IsStream
		info.IsStream = true
		defer func() { info.IsStream = originalIsStream }()

		rawBody, readErr := io.ReadAll(common.ReaderOnly(storage))
		if readErr != nil {
			return types.NewError(readErr, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.LogRequestTrace(c, info, "alpha-search-raw", c.Request.Method, c.Request.URL.String(), c.Request.Host, c.Request.Header, rawBody)
		responsesBody, convertErr := buildOpenAIResponsesSearchBody(rawBody, info, adaptor, c)
		if convertErr != nil {
			return types.NewError(convertErr, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		relaycommon.LogRequestTrace(c, info, "alpha-search-outbound-body", http.MethodPost, "/v1/responses", c.Request.Host, c.Request.Header, responsesBody)
		relaycommon.LogJSONRequestDiff(c, "alpha-search-raw-to-outbound", rawBody, responsesBody)
		requestBody, info.UpstreamRequestBodySize, requestBodyCloser, err = relaycommon.NewOutboundJSONBody(responsesBody)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
		}
		defer requestBodyCloser.Close()
	} else {
		info.UpstreamRequestBodySize = storage.Size()
		requestBody = common.ReaderOnly(storage)
	}

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	httpResp, ok := resp.(*http.Response)
	if !ok || httpResp == nil {
		return types.NewError(errors.New("invalid Codex search response"), types.ErrorCodeBadResponse)
	}
	logger.LogDebug(c, "codex search upstream response: status=%d content_type=%q content_length=%d transfer_encoding=%v",
		httpResp.StatusCode, httpResp.Header.Get("Content-Type"), httpResp.ContentLength, httpResp.TransferEncoding)
	if httpResp.StatusCode != http.StatusOK {
		newAPIError := service.RelayErrorHandler(c.Request.Context(), httpResp, false)
		service.ResetStatusCode(newAPIError, c.GetString("status_code_mapping"))
		return newAPIError
	}
	defer service.CloseResponseBodyGracefully(httpResp)

	if info.ChannelType == appconstant.ChannelTypeOpenAI {
		usage, responseErr := writeOpenAIResponsesSearchResult(c, info, httpResp)
		if responseErr != nil {
			return responseErr
		}
		service.PostTextConsumeQuota(c, info, usage, nil)
		return nil
	}

	responseBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	service.IOCopyBytesGracefully(c, httpResp, responseBody)
	return nil
}

type codexSearchForwardRequest struct {
	Input           json.RawMessage `json:"input,omitempty"`
	Commands        json.RawMessage `json:"commands,omitempty"`
	Settings        json.RawMessage `json:"settings,omitempty"`
	MaxOutputTokens *uint           `json:"max_output_tokens,omitempty"`
}

func buildOpenAIResponsesSearchBody(rawBody []byte, info *relaycommon.RelayInfo, adaptor interface {
	ConvertOpenAIResponsesRequest(*gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) (any, error)
}, c *gin.Context) ([]byte, error) {
	var searchRequest codexSearchForwardRequest
	if err := json.Unmarshal(rawBody, &searchRequest); err != nil {
		return nil, err
	}

	modelName := info.UpstreamModelName
	if modelName == "" {
		var baseRequest dto.CodexSearchRequest
		if err := json.Unmarshal(rawBody, &baseRequest); err != nil {
			return nil, err
		}
		modelName = baseRequest.Model
	}

	prompt := buildOpenAISearchPrompt(searchRequest)
	stream := true
	request := dto.OpenAIResponsesRequest{
		Model: modelName,
		Input: json.RawMessage(mustMarshalJSON([]map[string]any{
			{
				"type":             "message",
				"role":             "user",
				"additional_tools": []map[string]string{{"type": dto.BuildInToolWebSearch}},
				"content": []map[string]string{
					{"type": "input_text", "text": prompt},
				},
			},
		})),
		MaxOutputTokens: searchRequest.MaxOutputTokens,
		Store:           json.RawMessage("false"),
		Stream:          &stream,
	}
	if requestBody, marshalErr := json.Marshal(request); marshalErr == nil && c != nil && c.Request != nil {
		relaycommon.LogRequestTrace(c, info, "alpha-search-constructed-responses", http.MethodPost, "/v1/responses", c.Request.Host, c.Request.Header, requestBody)
	}
	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
	if err != nil {
		return nil, err
	}
	relaycommon.AppendRequestConversionFromRequest(info, converted)
	convertedBody, err := json.Marshal(converted)
	if err == nil && c != nil && c.Request != nil {
		relaycommon.LogRequestTrace(c, info, "alpha-search-after-adaptor", http.MethodPost, "/v1/responses", c.Request.Host, c.Request.Header, convertedBody)
	}
	return convertedBody, err
}

func buildOpenAISearchPrompt(request codexSearchForwardRequest) string {
	queries := extractCodexSearchQueries(request.Commands)
	if inputText := extractCodexSearchInput(request.Input); inputText != "" {
		queries = append(queries, inputText)
	}

	if len(queries) == 0 {
		queries = append(queries, "the requested topic")
	}
	return "Please search the web for: " + strings.Join(queries, "; ") +
		". Summarize the results and return only the final answer with source links."
}

func extractCodexSearchQueries(raw json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var commands struct {
		SearchQuery []struct {
			Query   string   `json:"q"`
			Domains []string `json:"domains"`
		} `json:"search_query"`
	}
	if err := json.Unmarshal(raw, &commands); err != nil {
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

func extractCodexSearchInput(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var textValue string
	if err := json.Unmarshal(raw, &textValue); err == nil {
		return strings.TrimSpace(textValue)
	}
	return ""
}

func mustMarshalJSON(value any) []byte {
	data, _ := json.Marshal(value)
	return data
}

type openAIResponsesSearchResult struct {
	Output string
	Usage  dto.Usage
}

func writeOpenAIResponsesSearchResult(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	logger.LogDebug(c, "codex search upstream response body: content_type=%q body_bytes=%d body=%s",
		resp.Header.Get("Content-Type"), len(responseBody), string(responseBody))
	// The upstream response is streamed internally, but this compatibility
	// endpoint buffers it into one JSON result for the alpha-search client.
	c.Writer.Header().Set("Content-Type", "application/json")
	c.Writer.Header().Del("Cache-Control")
	c.Writer.Header().Del("Connection")
	c.Writer.Header().Del("Transfer-Encoding")
	c.Writer.Header().Del("X-Accel-Buffering")

	result, parseErr := parseOpenAIResponsesSearchOutput(responseBody, resp.Header.Get("Content-Type"))
	if parseErr != nil {
		return nil, parseErr
	}
	normalizeOpenAIResponsesSearchUsage(info, &result)

	searchResponse, err := json.Marshal(struct {
		Output string `json:"output"`
	}{Output: result.Output})
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	c.Data(http.StatusOK, "application/json", searchResponse)
	return &result.Usage, nil
}

func parseOpenAIResponsesSearchOutput(responseBody []byte, contentType string) (openAIResponsesSearchResult, *types.NewAPIError) {
	if strings.Contains(strings.ToLower(contentType), "text/event-stream") || bytesContainDataEvent(responseBody) {
		return parseOpenAIResponsesSearchStream(responseBody)
	}

	var responsesResponse dto.OpenAIResponsesResponse
	if err := json.Unmarshal(responseBody, &responsesResponse); err != nil {
		return openAIResponsesSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return openAIResponsesSearchResult{}, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
	}
	return openAIResponsesSearchResult{
		Output: extractOpenAIResponsesText(responsesResponse),
		Usage:  openAIResponsesUsage(responsesResponse.Usage),
	}, nil
}

func parseOpenAIResponsesSearchStream(responseBody []byte) (openAIResponsesSearchResult, *types.NewAPIError) {
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
		if err := json.Unmarshal([]byte(data), &streamResponse); err != nil {
			return openAIResponsesSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
		}
		if streamResponse.Type == "response.output_text.delta" {
			output.WriteString(streamResponse.Delta)
		}
		if streamResponse.Type == "response.completed" && streamResponse.Response != nil {
			if oaiError := streamResponse.Response.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
				return openAIResponsesSearchResult{}, types.WithOpenAIError(*oaiError, http.StatusBadGateway)
			}
			usage = openAIResponsesUsage(streamResponse.Response.Usage)
			if output.Len() == 0 {
				output.WriteString(extractOpenAIResponsesText(*streamResponse.Response))
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return openAIResponsesSearchResult{}, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	return openAIResponsesSearchResult{Output: output.String(), Usage: usage}, nil
}

func openAIResponsesUsage(usage *dto.Usage) dto.Usage {
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

func normalizeOpenAIResponsesSearchUsage(info *relaycommon.RelayInfo, result *openAIResponsesSearchResult) {
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

func extractOpenAIResponsesText(response dto.OpenAIResponsesResponse) string {
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

func bytesContainDataEvent(body []byte) bool {
	return strings.Contains(string(body), "data:")
}
