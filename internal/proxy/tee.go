package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// rpcMessage is a minimal JSON-RPC 2.0 envelope for sniffing method and id.
type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

type callToolParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// RecordedCall is a single matched tools/call request + response.
type RecordedCall struct {
	Tool   string
	Args   map[string]any
	Result json.RawMessage // raw result from server
}

// SampledCall is a matched sampling/createMessage request + response.
// Params is the request from the server; Result is the client's LLM response.
type SampledCall struct {
	Params json.RawMessage
	Result json.RawMessage
}

// Interceptor tees two io streams, correlates tools/call pairs, and emits
// RecordedCall values on the Calls channel.
type Interceptor struct {
	Calls        chan RecordedCall
	SampledCalls chan SampledCall

	mu              sync.Mutex
	pending         map[string]callToolParams  // id → pending tools/call
	samplingPending map[string]json.RawMessage // id → pending sampling/createMessage params
}

func NewInterceptor() *Interceptor {
	return &Interceptor{
		Calls:           make(chan RecordedCall, 64),
		SampledCalls:    make(chan SampledCall, 64),
		pending:         make(map[string]callToolParams),
		samplingPending: make(map[string]json.RawMessage),
	}
}

// TeeClientToServer copies from client (host stdin) to server stdin, recording
// tools/call requests and sampling/createMessage responses.
func (ic *Interceptor) TeeClientToServer(dst io.Writer, src io.Reader) {
	ic.tee(dst, src, ic.snoopClientToServer, false)
}

// TeeServerToClient copies from server stdout to client (host stdout), recording
// tools/call responses and sampling/createMessage requests. Non-JSON lines
// (e.g. server startup logs) are redirected to stderr to avoid corrupting the stream.
func (ic *Interceptor) TeeServerToClient(dst io.Writer, src io.Reader) {
	ic.tee(dst, src, ic.snoopServerToClient, true)
}

func (ic *Interceptor) tee(dst io.Writer, src io.Reader, snoop func([]byte), filterNonJSON bool) {
	scanner := bufio.NewScanner(src)
	scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		snoop(line)
		if filterNonJSON && !bytes.HasPrefix(bytes.TrimSpace(line), []byte("{")) {
			fmt.Fprintf(os.Stderr, "[server] %s\n", line)
			continue
		}
		_, _ = dst.Write(line)
		_, _ = dst.Write([]byte{'\n'})
	}
}

// snoopClientToServer handles messages flowing host → server:
//   - tools/call requests (store in pending)
//   - sampling/createMessage responses (correlate with samplingPending, emit SampledCall)
func (ic *Interceptor) snoopClientToServer(line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}

	// sampling response: has id + result but no method
	if msg.Method == "" && msg.ID != nil && msg.Result != nil {
		key := string(msg.ID)
		ic.mu.Lock()
		params, ok := ic.samplingPending[key]
		if ok {
			delete(ic.samplingPending, key)
		}
		ic.mu.Unlock()
		if ok {
			ic.SampledCalls <- SampledCall{Params: params, Result: msg.Result}
		}
		return
	}

	if msg.Method != "tools/call" {
		return
	}
	var params callToolParams
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	ic.mu.Lock()
	ic.pending[string(msg.ID)] = params
	ic.mu.Unlock()
}

// snoopServerToClient handles messages flowing server → host:
//   - tools/call responses (correlate with pending, emit RecordedCall)
//   - sampling/createMessage requests (store in samplingPending)
func (ic *Interceptor) snoopServerToClient(line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}

	// sampling/createMessage request from server to client
	if msg.Method == "sampling/createMessage" && msg.ID != nil {
		ic.mu.Lock()
		ic.samplingPending[string(msg.ID)] = msg.Params
		ic.mu.Unlock()
		return
	}

	// tools/call response: has id + result, no method
	if msg.ID == nil || msg.Result == nil {
		return
	}
	key := string(msg.ID)
	ic.mu.Lock()
	params, ok := ic.pending[key]
	if ok {
		delete(ic.pending, key)
	}
	ic.mu.Unlock()
	if !ok {
		return
	}
	ic.Calls <- RecordedCall{
		Tool:   params.Name,
		Args:   params.Arguments,
		Result: msg.Result,
	}
}

// Close signals that no more calls will arrive.
func (ic *Interceptor) Close() {
	close(ic.Calls)
	close(ic.SampledCalls)
}
