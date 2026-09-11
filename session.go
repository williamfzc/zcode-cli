// Bridge session layer: pick the right renderer page target, hold the one
// CDP connection behind the cross-process control lock, and evaluate
// window.zcode bridge calls inside the renderer.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

type evalResult struct {
	Result           remoteObject     `json:"result"`
	ExceptionDetails *json.RawMessage `json:"exceptionDetails,omitempty"`
}

type remoteObject struct {
	Type        string          `json:"type"`
	Subtype     string          `json:"subtype,omitempty"`
	Description string          `json:"description,omitempty"`
	Value       json.RawMessage `json:"value,omitempty"`
}

type bridgeSession struct {
	client    *cdpClient
	sessionID string
	target    targetInfo
	lock      *os.File
}

var safeMethodPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func openBridgeSession(ctx context.Context, port int) (*bridgeSession, error) {
	lock, err := acquireControlLock(ctx)
	if err != nil {
		return nil, err
	}
	session, err := openBridgeSessionLocked(ctx, port)
	if err != nil {
		releaseControlLock(lock)
		return nil, err
	}
	session.lock = lock
	return session, nil
}

func openBridgeSessionLocked(ctx context.Context, port int) (*bridgeSession, error) {
	client, err := connectCDP(ctx, port)
	if err != nil {
		return nil, err
	}
	session, err := findBridgeSession(client)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return session, nil
}

func (s *bridgeSession) Close() error {
	err := s.client.Close()
	releaseControlLock(s.lock)
	return err
}

func findBridgeSession(client *cdpClient) (*bridgeSession, error) {
	sessions := make([]*bridgeSession, 0, len(client.sessions))
	for id, target := range client.sessions {
		if target.Type == "page" {
			sessions = append(sessions, &bridgeSession{client: client, sessionID: id, target: target})
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		return pagePriority(sessions[i].target.URL) > pagePriority(sessions[j].target.URL)
	})

	var lastErr error
	for _, session := range sessions {
		var probe struct {
			Has         bool   `json:"has"`
			URL         string `json:"url"`
			Ready       string `json:"ready"`
			MethodCount int    `json:"methodCount"`
		}
		err := session.evaluateInto(`(() => {
  const z = window.zcode;
  return {
    has: Boolean(z),
    url: location.href,
    ready: document.readyState,
    methodCount: z ? Object.keys(z).length : 0
  };
})()`, &probe)
		if err != nil {
			lastErr = err
			continue
		}
		if probe.Has {
			session.target.URL = probe.URL
			return session, nil
		}
	}
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no page targets found; start ZCode with %s first", debugStartHint())
	}
	if lastErr != nil {
		return nil, fmt.Errorf("window.zcode was not available on any page (%w); run %s if needed", lastErr, debugStartHint())
	}
	return nil, fmt.Errorf("window.zcode was not found; start ZCode with %s first", debugStartHint())
}

func pagePriority(url string) int {
	score := 0
	if strings.HasPrefix(url, "file://") {
		score += 10
	}
	if strings.Contains(url, "/index.html") {
		score += 20
	}
	if strings.Contains(strings.ToLower(url), "zcode") {
		score += 5
	}
	if strings.HasPrefix(url, "devtools://") {
		score -= 100
	}
	return score
}

func (s *bridgeSession) evaluate(expression string) (json.RawMessage, error) {
	return s.evaluateTimeout(expression, 30*time.Second)
}

func (s *bridgeSession) evaluateTimeout(expression string, timeout time.Duration) (json.RawMessage, error) {
	raw, err := s.client.callTimeout("Runtime.evaluate", map[string]any{
		"expression":            expression,
		"awaitPromise":          true,
		"returnByValue":         true,
		"userGesture":           false,
		"includeCommandLineAPI": false,
	}, s.sessionID, timeout)
	if err != nil {
		return nil, err
	}
	var result evalResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("decode evaluation result: %w", err)
	}
	if result.ExceptionDetails != nil {
		return nil, formatException(*result.ExceptionDetails)
	}
	if result.Result.Type == "undefined" {
		return json.RawMessage("null"), nil
	}
	if len(result.Result.Value) == 0 {
		return nil, fmt.Errorf("evaluation returned non-serializable %s %s", result.Result.Type, result.Result.Description)
	}
	return result.Result.Value, nil
}

func (s *bridgeSession) evaluateInto(expression string, dest any) error {
	raw, err := s.evaluate(expression)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return fmt.Errorf("decode evaluation value: %w", err)
	}
	return nil
}

func (s *bridgeSession) callZCode(method string) (json.RawMessage, error) {
	if !safeMethodPattern.MatchString(method) {
		return nil, fmt.Errorf("unsafe bridge method name %q", method)
	}
	expression := fmt.Sprintf(`(async () => {
  const fn = window.zcode[%q];
  if (typeof fn !== 'function') {
    const error = new Error('window.zcode.%s is not a function');
    error.name = 'MissingBridgeMethod';
    throw error;
  }
  return await fn.call(window.zcode);
})()`, method, method)
	return s.evaluate(expression)
}

func formatException(raw json.RawMessage) error {
	var details struct {
		Text      string `json:"text"`
		Exception struct {
			Description string `json:"description"`
		} `json:"exception"`
	}
	if err := json.Unmarshal(raw, &details); err == nil {
		if details.Exception.Description != "" {
			return fmt.Errorf("bridge call failed: %s", strings.TrimSpace(details.Exception.Description))
		}
		if details.Text != "" {
			return fmt.Errorf("bridge call failed: %s", details.Text)
		}
	}
	return fmt.Errorf("bridge call failed: %s", string(raw))
}

func debugStartHint() string { return "zcodecli start" }
