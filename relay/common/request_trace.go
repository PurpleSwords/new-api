package common

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"

	rootcommon "github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
)

const (
	requestTraceBodyLimit = 1024 * 1024
	requestTraceDiffLimit = 128
)

// LogRequestTrace records one request stage in a deterministic, redacted form.
// It is intentionally debug-only because request bodies can contain user data.
func LogRequestTrace(ctx context.Context, info *RelayInfo, stage, method, targetURL, host string, headers http.Header, body []byte) {
	if !rootcommon.DebugEnabled {
		return
	}

	logger.LogDebug(ctx, "request trace: stage=%s method=%s target=%s host=%q relay_mode=%d relay_format=%v request_path=%q channel_id=%d channel_type=%d api_type=%d retry=%d origin_model=%q upstream_model=%q is_stream=%t body_bytes=%d body_sha256=%s headers=%s body=%s",
		stage, method, SanitizeURLForLog(targetURL), host,
		relayMode(info), relayFormat(info), requestPath(info), channelID(info), channelType(info), apiType(info), retryIndex(info),
		originModel(info), upstreamModel(info), isStream(info), len(body), requestBodySHA256(body), formatTraceHeaders(headers), formatTraceBody(body))
}

// LogJSONRequestDiff reports semantic JSON changes between two request stages.
// The full stage bodies are logged separately by LogRequestTrace.
func LogJSONRequestDiff(ctx context.Context, stage string, before, after []byte) {
	if !rootcommon.DebugEnabled {
		return
	}

	var beforeValue, afterValue any
	if err := json.Unmarshal(before, &beforeValue); err != nil {
		logger.LogDebug(ctx, "request trace diff: stage=%s parse_before_error=%q before_sha256=%s after_sha256=%s",
			stage, err.Error(), requestBodySHA256(before), requestBodySHA256(after))
		return
	}
	if err := json.Unmarshal(after, &afterValue); err != nil {
		logger.LogDebug(ctx, "request trace diff: stage=%s parse_after_error=%q before_sha256=%s after_sha256=%s",
			stage, err.Error(), requestBodySHA256(before), requestBodySHA256(after))
		return
	}

	diffs := make([]string, 0)
	collectJSONDiffs(beforeValue, afterValue, "$", &diffs)
	if len(diffs) == 0 {
		diffs = append(diffs, "<no semantic change>")
	} else if len(diffs) > requestTraceDiffLimit {
		diffs = append(diffs[:requestTraceDiffLimit], fmt.Sprintf("... truncated, total=%d", len(diffs)))
	}
	logger.LogDebug(ctx, "request trace diff: stage=%s before_bytes=%d after_bytes=%d before_sha256=%s after_sha256=%s changed_paths=%s",
		stage, len(before), len(after), requestBodySHA256(before), requestBodySHA256(after), strings.Join(diffs, ","))
}

func formatTraceHeaders(headers http.Header) string {
	if len(headers) == 0 {
		return "{}"
	}
	values := make(map[string][]string, len(headers))
	for name, headerValues := range headers {
		if isSensitiveTraceHeader(name) {
			values[name] = []string{"***masked***"}
			continue
		}
		values[name] = append([]string(nil), headerValues...)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return fmt.Sprintf("<header marshal error: %s>", err)
	}
	return string(encoded)
}

func isSensitiveTraceHeader(name string) bool {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "authorization" || normalized == "proxy-authorization" || normalized == "cookie" || normalized == "set-cookie" {
		return true
	}
	for _, marker := range []string{"api-key", "apikey", "token", "secret", "credential", "password"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func formatTraceBody(body []byte) string {
	if len(body) == 0 {
		return "<empty>"
	}

	shown := body
	truncated := false
	if len(shown) > requestTraceBodyLimit {
		shown = shown[:requestTraceBodyLimit]
		truncated = true
	}

	var compact bytes.Buffer
	if json.Compact(&compact, shown) == nil {
		shown = compact.Bytes()
	}
	if truncated {
		return fmt.Sprintf("%s... [truncated, original_length=%d, limit=%d]", shown, len(body), requestTraceBodyLimit)
	}
	return string(shown)
}

func requestBodySHA256(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func collectJSONDiffs(before, after any, path string, diffs *[]string) {
	if len(*diffs) > requestTraceDiffLimit {
		return
	}

	switch beforeValue := before.(type) {
	case map[string]any:
		afterValue, ok := after.(map[string]any)
		if !ok {
			*diffs = append(*diffs, path+": type changed")
			return
		}
		keys := make(map[string]struct{}, len(beforeValue)+len(afterValue))
		for key := range beforeValue {
			keys[key] = struct{}{}
		}
		for key := range afterValue {
			keys[key] = struct{}{}
		}
		sortedKeys := make([]string, 0, len(keys))
		for key := range keys {
			sortedKeys = append(sortedKeys, key)
		}
		sort.Strings(sortedKeys)
		for _, key := range sortedKeys {
			beforeItem, beforeOK := beforeValue[key]
			afterItem, afterOK := afterValue[key]
			itemPath := path + "." + key
			switch {
			case !beforeOK:
				*diffs = append(*diffs, itemPath+": added")
			case !afterOK:
				*diffs = append(*diffs, itemPath+": removed")
			default:
				collectJSONDiffs(beforeItem, afterItem, itemPath, diffs)
			}
		}
	case []any:
		afterValue, ok := after.([]any)
		if !ok {
			*diffs = append(*diffs, path+": type changed")
			return
		}
		if len(beforeValue) != len(afterValue) {
			*diffs = append(*diffs, fmt.Sprintf("%s: length %d->%d", path, len(beforeValue), len(afterValue)))
		}
		for index := 0; index < len(beforeValue) && index < len(afterValue); index++ {
			collectJSONDiffs(beforeValue[index], afterValue[index], fmt.Sprintf("%s[%d]", path, index), diffs)
		}
	default:
		if !reflect.DeepEqual(before, after) {
			*diffs = append(*diffs, path+": value changed")
		}
	}
}

func relayMode(info *RelayInfo) int {
	if info == nil {
		return 0
	}
	return info.RelayMode
}

func relayFormat(info *RelayInfo) any {
	if info == nil {
		return ""
	}
	return info.RelayFormat
}

func requestPath(info *RelayInfo) string {
	if info == nil {
		return ""
	}
	return info.RequestURLPath
}

func channelID(info *RelayInfo) int {
	if info == nil || info.ChannelMeta == nil {
		return 0
	}
	return info.ChannelId
}

func channelType(info *RelayInfo) int {
	if info == nil || info.ChannelMeta == nil {
		return 0
	}
	return info.ChannelType
}

func apiType(info *RelayInfo) int {
	if info == nil || info.ChannelMeta == nil {
		return 0
	}
	return info.ApiType
}

func retryIndex(info *RelayInfo) int {
	if info == nil {
		return 0
	}
	return info.RetryIndex
}

func originModel(info *RelayInfo) string {
	if info == nil {
		return ""
	}
	return info.OriginModelName
}

func upstreamModel(info *RelayInfo) string {
	if info == nil || info.ChannelMeta == nil {
		return ""
	}
	return info.UpstreamModelName
}

func isStream(info *RelayInfo) bool {
	if info == nil {
		return false
	}
	return info.IsStream
}
