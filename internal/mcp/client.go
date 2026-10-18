package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/arthurkushman/subcoder/internal/provider"
)

const protocolVersion = "2024-11-05"

type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type Client interface {
	Name() string
	ListTools(ctx context.Context) ([]ToolInfo, error)
	CallTool(ctx context.Context, name string, args json.RawMessage) (string, error)
	Close() error
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string {
	return fmt.Sprintf("mcp: rpc error %d: %s", e.Code, e.Message)
}

type StdioClient struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader

	mu     sync.Mutex
	nextID int
	closed bool
}

func StartStdio(ctx context.Context, name string, command []string, env map[string]string) (*StdioClient, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("mcp: server %q has no command", name)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Env = os.Environ()
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", name, err)
	}

	client := &StdioClient{
		name:   name,
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}

	if err := client.initialize(ctx); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *StdioClient) Name() string { return c.name }

func (c *StdioClient) initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "subcoder", "version": "1.0.0"},
	})
	if err != nil {
		return err
	}
	return c.notify("notifications/initialized", map[string]any{})
}

func (c *StdioClient) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeLocked(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *StdioClient) writeLocked(request rpcRequest) error {
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if _, err := c.stdin.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("mcp: write to %q: %w", c.name, err)
	}
	return nil
}

func (c *StdioClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil, fmt.Errorf("mcp: client %q is closed", c.name)
	}
	c.nextID++
	request := rpcRequest{JSONRPC: "2.0", ID: c.nextID, Method: method, Params: params}
	if err := c.writeLocked(request); err != nil {
		return nil, err
	}

	type result struct {
		data json.RawMessage
		err  error
	}
	done := make(chan result, 1)
	go func() {
		response, err := c.readResponse(request.ID)
		if err != nil {
			done <- result{err: err}
			return
		}
		done <- result{data: response.Result}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-done:
		return res.data, res.err
	}
}

func (c *StdioClient) readResponse(id int) (rpcResponse, error) {
	for {
		line, err := c.reader.ReadBytes('\n')
		if err != nil {
			return rpcResponse{}, fmt.Errorf("mcp: read from %q: %w", c.name, err)
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var response rpcResponse
		if err := json.Unmarshal(line, &response); err != nil {
			continue
		}
		if response.ID != id {
			continue
		}
		if response.Error != nil {
			return rpcResponse{}, response.Error
		}
		return response, nil
	}
}

func (c *StdioClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	data, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("mcp: decode tools/list: %w", err)
	}
	return payload.Tools, nil
}

func (c *StdioClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var arguments any
	if err := json.Unmarshal(args, &arguments); err != nil {
		arguments = map[string]any{}
	}
	data, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	return decodeToolResult(data)
}

func (c *StdioClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

func decodeToolResult(data json.RawMessage) (string, error) {
	var payload struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return string(data), nil
	}

	var texts []string
	for _, block := range payload.Content {
		if block.Text != "" {
			texts = append(texts, block.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if payload.IsError {
		return "", fmt.Errorf("mcp: tool error: %s", text)
	}
	if text == "" {
		text = string(data)
	}
	return text, nil
}

type HTTPClient struct {
	name      string
	url       string
	sessionID string
	client    *http.Client

	mu     sync.Mutex
	nextID int
}

func NewHTTPClient(ctx context.Context, name, url string) (*HTTPClient, error) {
	client := &HTTPClient{name: name, url: url, client: &http.Client{}}
	if err := client.initialize(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *HTTPClient) Name() string { return c.name }

func (c *HTTPClient) initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "subcoder", "version": "1.0.0"},
	})
	if err != nil {
		return err
	}
	_, _ = c.call(ctx, "notifications/initialized", map[string]any{})
	return nil
}

func (c *HTTPClient) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mcp: %s: %w", c.name, err)
	}
	defer resp.Body.Close()

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("mcp: %s: %s: %s", c.name, resp.Status, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode == http.StatusAccepted || resp.StatusCode == http.StatusNoContent {
		return json.RawMessage(`{}`), nil
	}

	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		var result json.RawMessage
		err := provider.ReadSSE(ctx, resp.Body, func(_ string, data []byte) error {
			var response rpcResponse
			if err := json.Unmarshal(data, &response); err != nil {
				return nil
			}
			if response.ID != id {
				return nil
			}
			if response.Error != nil {
				return response.Error
			}
			result = response.Result
			return nil
		})
		if err != nil {
			return nil, err
		}
		if result == nil {
			return nil, fmt.Errorf("mcp: %s: no response for request %d", c.name, id)
		}
		return result, nil
	}

	var response rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("mcp: %s: decode response: %w", c.name, err)
	}
	if response.Error != nil {
		return nil, response.Error
	}
	return response.Result, nil
}

func (c *HTTPClient) ListTools(ctx context.Context) ([]ToolInfo, error) {
	data, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var payload struct {
		Tools []ToolInfo `json:"tools"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("mcp: decode tools/list: %w", err)
	}
	return payload.Tools, nil
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	var arguments any
	if err := json.Unmarshal(args, &arguments); err != nil {
		arguments = map[string]any{}
	}
	data, err := c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		return "", err
	}
	return decodeToolResult(data)
}

func (c *HTTPClient) Close() error { return nil }
