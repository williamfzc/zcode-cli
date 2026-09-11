// Minimal Chrome DevTools Protocol client: HTTP version probe, a single
// WebSocket connection, timed JSON-RPC calls, and page-target tracking.
// The lowest layer; all ZCode access goes through it.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const defaultDebugPort = 9227

type versionInfo struct {
	Browser              string `json:"Browser"`
	ProtocolVersion      string `json:"Protocol-Version"`
	UserAgent            string `json:"User-Agent"`
	V8Version            string `json:"V8-Version"`
	WebKitVersion        string `json:"WebKit-Version"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type targetInfo struct {
	TargetID        string `json:"targetId"`
	Type            string `json:"type"`
	Title           string `json:"title"`
	URL             string `json:"url"`
	Attached        bool   `json:"attached"`
	CanAccessOpener bool   `json:"canAccessOpener"`
}

type cdpClient struct {
	conn     *websocket.Conn
	baseURL  string
	nextID   int64
	mu       sync.Mutex
	sessions map[string]targetInfo
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("CDP error %d: %s", e.Code, e.Message) }

type cdpRequest struct {
	ID        int64           `json:"id"`
	Method    string          `json:"method"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
}

type cdpResponse struct {
	ID        int64           `json:"id"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *cdpError       `json:"error,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
}

func fetchVersion(ctx context.Context, port int) (*versionInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/json/version", port), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("debug endpoint returned HTTP %d", resp.StatusCode)
	}
	var info versionInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decode debug version: %w", err)
	}
	if info.WebSocketDebuggerURL == "" {
		return nil, errors.New("debug endpoint did not provide webSocketDebuggerUrl")
	}
	return &info, nil
}

func connectCDP(ctx context.Context, port int) (*cdpClient, error) {
	info, err := fetchVersion(ctx, port)
	if err != nil {
		return nil, fmt.Errorf("%w; run zcodecli start to launch ZCode with the debug port", err)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, _, err := dialer.DialContext(ctx, info.WebSocketDebuggerURL, nil)
	if err != nil {
		return nil, fmt.Errorf("connect CDP websocket: %w", err)
	}
	client := &cdpClient{
		conn:     conn,
		baseURL:  fmt.Sprintf("http://127.0.0.1:%d", port),
		sessions: make(map[string]targetInfo),
	}
	if _, err := client.call("Target.setAutoAttach", map[string]any{
		"autoAttach":             true,
		"waitForDebuggerOnStart": false,
		"flatten":                true,
	}, ""); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := client.refreshTargets(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return client, nil
}

func (c *cdpClient) Close() error { return c.conn.Close() }

func (c *cdpClient) call(method string, params any, sessionID string) (json.RawMessage, error) {
	return c.callTimeout(method, params, sessionID, 30*time.Second)
}

func (c *cdpClient) callTimeout(method string, params any, sessionID string, timeout time.Duration) (json.RawMessage, error) {
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.mu.Unlock()

	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		rawParams = b
	}
	req := cdpRequest{ID: id, Method: method, Params: rawParams, SessionID: sessionID}
	c.mu.Lock()
	if err := c.conn.WriteJSON(req); err != nil {
		c.mu.Unlock()
		return nil, fmt.Errorf("write CDP request: %w", err)
	}
	c.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for {
		var msg []byte
		c.mu.Lock()
		_ = c.conn.SetReadDeadline(deadline)
		_, msg, err := c.conn.ReadMessage()
		c.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("read CDP response: %w", err)
		}
		var envelope cdpResponse
		if err := json.Unmarshal(msg, &envelope); err != nil {
			return nil, fmt.Errorf("decode CDP message: %w", err)
		}
		if envelope.Method == "Target.attachedToTarget" {
			c.recordAttached(envelope.Params)
		}
		if envelope.ID == id {
			if envelope.Error != nil {
				return nil, envelope.Error
			}
			return envelope.Result, nil
		}
	}
}

type getTargetsResult struct {
	TargetInfos []targetInfo `json:"targetInfos"`
}

type attachResult struct {
	SessionID string `json:"sessionId"`
}

type attachedParams struct {
	SessionID  string     `json:"sessionId"`
	TargetInfo targetInfo `json:"targetInfo"`
}

func (c *cdpClient) refreshTargets() error {
	raw, err := c.call("Target.getTargets", nil, "")
	if err != nil {
		return err
	}
	var result getTargetsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode targets: %w", err)
	}
	for _, target := range result.TargetInfos {
		if target.Type != "page" || target.Attached {
			continue
		}
		raw, err := c.call("Target.attachToTarget", map[string]any{
			"targetId": target.TargetID,
			"flatten":  true,
		}, "")
		if err != nil {
			continue
		}
		var attached attachResult
		if err := json.Unmarshal(raw, &attached); err != nil {
			continue
		}
		target.Attached = true
		c.sessions[attached.SessionID] = target
	}
	return nil
}

func (c *cdpClient) recordAttached(raw json.RawMessage) {
	var params attachedParams
	if err := json.Unmarshal(raw, &params); err != nil || params.SessionID == "" {
		return
	}
	c.sessions[params.SessionID] = params.TargetInfo
}
