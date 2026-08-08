package relay

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeResponsesWSCreateEventWrapper(t *testing.T) {
	message := []byte(`{
		"type": "response.create",
		"event_id": "evt_1",
		"generate": false,
		"response": {
			"model": "gpt-5.3-codex-spark",
			"input": "hi",
			"store": false,
			"stream": true,
			"stream_options": {"include_usage": true}
		}
	}`)

	create, eventID, err := normalizeResponsesWSCreateEvent(message)
	if err != nil {
		t.Fatalf("normalizeResponsesWSCreateEvent() error = %v", err)
	}
	req := create.Request
	if eventID != "evt_1" {
		t.Fatalf("eventID = %q, want evt_1", eventID)
	}
	if req.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("model = %q", req.Model)
	}
	if strings.TrimSpace(string(create.Generate)) != "false" {
		t.Fatalf("generate = %s, want false", create.Generate)
	}
	if req.Stream != nil {
		t.Fatalf("stream = %v, want nil", req.Stream)
	}
	if req.StreamOptions != nil {
		t.Fatalf("stream_options = %#v, want nil", req.StreamOptions)
	}
	if strings.TrimSpace(string(req.Store)) != "false" {
		t.Fatalf("store = %s, want false", req.Store)
	}
}

func TestResponsesWebSocketUsesTokenFallbackWithoutWebSearchSurcharge(t *testing.T) {
	tool := &relaycommon.BuildInToolInfo{ToolName: dto.BuildInToolWebSearchPreview}
	info := &relaycommon.RelayInfo{
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
			BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
				dto.BuildInToolWebSearchPreview: tool,
			},
		},
	}
	info.SetEstimatePromptTokens(17)
	state := &responsesWSCallState{info: info, usage: &dto.Usage{}}
	session := &responsesWSSession{
		current: state,
	}

	session.observeUpstreamMessage([]byte(`{
		"type":"response.output_item.done",
		"item":{"type":"web_search_call"}
	}`))

	if tool.CallCount != 0 {
		t.Fatalf("web search call count = %d, want 0", tool.CallCount)
	}

	finalizeResponsesWSUsage(state)
	if state.usage.PromptTokens != 17 || state.usage.TotalTokens != 17 {
		t.Fatalf("usage = %+v, want 17 estimated input tokens and no surcharge", state.usage)
	}
}

func TestNormalizeResponsesWSCreateEventFlat(t *testing.T) {
	message := []byte(`{
		"type": "response.create",
		"event_id": "evt_2",
		"model": "gpt-5.3-codex-spark",
		"input": "hi",
		"generate": false,
		"stream": true,
		"background": true,
		"stream_options": {"include_usage": true}
	}`)

	create, eventID, err := normalizeResponsesWSCreateEvent(message)
	if err != nil {
		t.Fatalf("normalizeResponsesWSCreateEvent() error = %v", err)
	}
	req := create.Request
	if eventID != "evt_2" {
		t.Fatalf("eventID = %q, want evt_2", eventID)
	}
	if req.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("model = %q", req.Model)
	}
	if strings.TrimSpace(string(create.Generate)) != "false" {
		t.Fatalf("generate = %s, want false", create.Generate)
	}
	if req.Stream != nil {
		t.Fatalf("stream = %v, want nil", req.Stream)
	}
	if req.StreamOptions != nil {
		t.Fatalf("stream_options = %#v, want nil", req.StreamOptions)
	}
}

func TestBuildResponsesWSCreateEventIsFlat(t *testing.T) {
	payload := []byte(`{
		"model": "gpt-5.3-codex-spark",
		"input": "hi",
		"store": false,
		"event_id": "evt_upstream",
		"stream": true,
		"background": true,
		"stream_options": {"include_usage": true}
	}`)

	got, err := buildResponsesWSCreateEvent(payload, common.RawMessage(`false`))
	if err != nil {
		t.Fatalf("buildResponsesWSCreateEvent() error = %v", err)
	}
	var data map[string]any
	if err := common.Unmarshal(got, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if data["type"] != responsesWSEventTypeResponseCreate {
		t.Fatalf("type = %#v", data["type"])
	}
	if data["model"] != "gpt-5.3-codex-spark" || data["input"] != "hi" || data["store"] != false {
		t.Fatalf("unexpected flat event fields: %s", got)
	}
	if data["generate"] != false {
		t.Fatalf("generate = %#v, want false", data["generate"])
	}
	for _, key := range []string{"response", "event_id", "stream", "background", "stream_options"} {
		if _, ok := data[key]; ok {
			t.Fatalf("field %q should not be present in upstream event: %s", key, got)
		}
	}
}

func TestHTTPResponsesRequestDoesNotMarshalGenerate(t *testing.T) {
	var req dto.OpenAIResponsesRequest
	if err := common.Unmarshal([]byte(`{"model":"gpt-5.3-codex-spark","input":"hi","generate":false}`), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	got, err := common.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var data map[string]any
	if err := common.Unmarshal(got, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if _, ok := data["generate"]; ok {
		t.Fatalf("generate leaked into HTTP request JSON: %s", got)
	}
}

func TestBuildResponsesWSErrorPayloadIncludesStatus(t *testing.T) {
	payload, err := buildResponsesWSErrorPayload("evt_err", types.NewErrorWithStatusCode(
		errors.New("model is required"),
		types.ErrorCodeInvalidRequest,
		http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	))
	if err != nil {
		t.Fatalf("buildResponsesWSErrorPayload() error = %v", err)
	}
	var data struct {
		Type    string             `json:"type"`
		Status  int                `json:"status"`
		EventID string             `json:"event_id"`
		Error   *types.OpenAIError `json:"error"`
	}
	if err := common.Unmarshal(payload, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if data.Type != "error" || data.Status != http.StatusBadRequest || data.EventID != "evt_err" {
		t.Fatalf("unexpected error event: %s", payload)
	}
	if data.Error == nil || data.Error.Code != string(types.ErrorCodeInvalidRequest) {
		t.Fatalf("unexpected error body: %#v", data.Error)
	}
}

func TestResponsesWSInvalidRequestErrorUsesBadRequestStatus(t *testing.T) {
	payload, err := buildResponsesWSErrorPayload("", newResponsesWSInvalidRequestError(errors.New("bad event")))
	if err != nil {
		t.Fatalf("buildResponsesWSErrorPayload() error = %v", err)
	}
	var data struct {
		Status int `json:"status"`
	}
	if err := common.Unmarshal(payload, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if data.Status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", data.Status, http.StatusBadRequest)
	}
}

func TestRemoveResponsesWSTransportFields(t *testing.T) {
	payload := []byte(`{
		"model": "gpt-5.3-codex-spark",
		"stream": true,
		"background": true,
		"stream_options": {"include_usage": true},
		"store": false
	}`)

	got, err := removeResponsesWSTransportFields(payload)
	if err != nil {
		t.Fatalf("removeResponsesWSTransportFields() error = %v", err)
	}
	var data map[string]any
	if err := common.Unmarshal(got, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	for _, key := range []string{"stream", "background", "stream_options"} {
		if _, ok := data[key]; ok {
			t.Fatalf("transport field %q still present in %s", key, got)
		}
	}
	if data["store"] != false {
		t.Fatalf("store = %#v, want false", data["store"])
	}
}

func TestToWebSocketURL(t *testing.T) {
	tests := map[string]string{
		"https://api.openai.com/v1/responses":             "wss://api.openai.com/v1/responses",
		"http://127.0.0.1:3000/v1/responses":              "ws://127.0.0.1:3000/v1/responses",
		"wss://chatgpt.com/backend-api/codex/responses":   "wss://chatgpt.com/backend-api/codex/responses",
		"ws://127.0.0.1:3000/backend-api/codex/responses": "ws://127.0.0.1:3000/backend-api/codex/responses",
	}

	for input, want := range tests {
		if got := toWebSocketURL(input); got != want {
			t.Fatalf("toWebSocketURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestHandleTargetWriteFailureWithStateReleasesCurrentAndClearsTarget(t *testing.T) {
	target, cleanup := newTestResponsesWSTarget(t)
	defer cleanup()

	var committed *bool
	session := &responsesWSSession{target: target}
	state := &responsesWSCallState{
		info: &relaycommon.RelayInfo{},
		commitRate: func(success bool) {
			committed = &success
		},
	}
	session.current = state

	apiErr := session.handleTargetWriteFailureWithState(state, errors.New("write failed"))

	if apiErr == nil {
		t.Fatal("apiErr is nil")
	}
	if session.target != nil {
		t.Fatal("target was not cleared")
	}
	if session.getCurrent() != nil {
		t.Fatal("current response was not released")
	}
	if committed == nil || *committed {
		t.Fatalf("commit success = %v, want false", committed)
	}
}

func TestHandleControlEventWriteFailureSendsResponsesError(t *testing.T) {
	clientConn, serverConn, cleanupClient := newTestWebSocketPair(t)
	defer cleanupClient()
	target, cleanupTarget := newTestResponsesWSTarget(t)
	defer cleanupTarget()

	session := &responsesWSSession{
		client: serverConn,
		target: target,
	}
	apiErr := session.handleControlEventWriteFailure(errors.New("write failed"))
	if apiErr != nil {
		t.Fatalf("handleControlEventWriteFailure() error = %v", apiErr)
	}
	if session.target != nil {
		t.Fatal("target was not cleared")
	}

	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, payload, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("read responses error event: %v", err)
	}
	var data struct {
		Type   string `json:"type"`
		Status int    `json:"status"`
	}
	if err := common.Unmarshal(payload, &data); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if data.Type != "error" || data.Status == 0 {
		t.Fatalf("unexpected error event: %s", payload)
	}
}

func TestObserveUpstreamFailedReleasesCurrent(t *testing.T) {
	var committed *bool
	session := &responsesWSSession{}
	state := &responsesWSCallState{
		info: &relaycommon.RelayInfo{},
		commitRate: func(success bool) {
			committed = &success
		},
	}
	session.current = state

	session.observeUpstreamMessage([]byte(`{"type":"response.failed"}`))

	if session.getCurrent() != nil {
		t.Fatal("current response was not released")
	}
	if committed == nil || *committed {
		t.Fatalf("commit success = %v, want false", committed)
	}
}

func TestResponsesWSClientKeepaliveSendsPingAndAcceptsPong(t *testing.T) {
	clientConn, serverConn, cleanup := newTestWebSocketPair(t)
	defer cleanup()

	pingReceived := make(chan struct{}, 8)
	clientConn.SetPingHandler(func(data string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return clientConn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	stopKeepalive, err := startResponsesWSClientKeepalive(serverConn, 50*time.Millisecond)
	require.NoError(t, err)
	defer stopKeepalive()

	type readResult struct {
		messageType int
		message     []byte
		err         error
	}
	readResultCh := make(chan readResult, 1)
	go func() {
		messageType, message, err := serverConn.ReadMessage()
		readResultCh <- readResult{messageType: messageType, message: message, err: err}
	}()

	for range 4 {
		select {
		case <-pingReceived:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for websocket ping")
		}
	}
	require.NoError(t, clientConn.WriteMessage(websocket.TextMessage, []byte("still-alive")))

	select {
	case result := <-readResultCh:
		require.NoError(t, result.err)
		assert.Equal(t, websocket.TextMessage, result.messageType)
		assert.Equal(t, []byte("still-alive"), result.message)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message after pong refresh")
	}
}

func TestResponsesWSClientKeepaliveTimesOutWithoutPong(t *testing.T) {
	clientConn, serverConn, cleanup := newTestWebSocketPair(t)
	defer cleanup()

	pingReceived := make(chan struct{}, 1)
	clientConn.SetPingHandler(func(string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		for {
			if _, _, err := clientConn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	stopKeepalive, err := startResponsesWSClientKeepalive(serverConn, 50*time.Millisecond)
	require.NoError(t, err)
	defer stopKeepalive()

	readErrCh := make(chan error, 1)
	go func() {
		_, _, err := serverConn.ReadMessage()
		readErrCh <- err
	}()

	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for websocket ping")
	}
	select {
	case err := <-readErrCh:
		require.Error(t, err)
		var netErr net.Error
		require.ErrorAs(t, err, &netErr)
		assert.True(t, netErr.Timeout())
	case <-time.After(time.Second):
		t.Fatal("connection without pong did not reach its read deadline")
	}
}

func newTestResponsesWSTarget(t *testing.T) (*websocket.Conn, func()) {
	t.Helper()
	target, _, cleanup := newTestWebSocketPair(t)
	return target, cleanup
}

func newTestWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn, func()) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	serverConnCh := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		serverConnCh <- conn
	}))

	targetURL := "ws" + strings.TrimPrefix(server.URL, "http")
	target, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		server.Close()
		t.Fatalf("dial websocket: %v", err)
	}
	serverConn := <-serverConnCh
	cleanup := func() {
		_ = target.Close()
		_ = serverConn.Close()
		server.Close()
	}
	return target, serverConn, cleanup
}
