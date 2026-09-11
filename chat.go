// Chat domain: list/show/new/send that drive the agent. Reaches the renderer's
// workspace services by walking the React fiber tree, submits
// sendConversationCommandV4 envelopes with the renderer-registered client id,
// and polls getTaskSnapshot until the finished turn is collected.

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

// chatReplyTimeout bounds how long a chat new/send waits for the agent to finish
// a turn. Agent turns (especially ones that run tools) can take several minutes.
const chatReplyTimeout = 10 * time.Minute

// chatPollInterval is the gap between task snapshot polls while a turn runs.
const chatPollInterval = 2 * time.Second

// chatDefaultMessageLimit is the page size for `chat show` (recent tail).
const chatDefaultMessageLimit = 20

// chatPrelude locates the active workspace's service bag by walking the React
// fiber tree. The window.zcode bridge exposes no chat methods, and the services
// are RPC proxies reachable only from inside the renderer. No globals, hooks,
// or wrappers are installed; the walk is repeated for every command.
const chatPrelude = `
function __zFindBag() {
  var roots = [];
  var seenFiber = new Set();
  var nodes = document.querySelectorAll('*');
  for (var ni = 0; ni < nodes.length; ni++) {
    var el = nodes[ni];
    var keys = Object.keys(el);
    for (var ki = 0; ki < keys.length; ki++) {
      var key = keys[ki];
      if ((key.indexOf('__reactFiber$') === 0 || key.indexOf('__reactContainer$') === 0) && el[key] && !seenFiber.has(el[key])) {
        seenFiber.add(el[key]);
        roots.push(el[key]);
      }
    }
  }
  function isObj(v) { return v !== null && typeof v === 'object'; }
  var visited = new Set();
  var bag = null;
  function deepFind(obj, depth) {
    if (bag || !isObj(obj) || depth > 6) return;
    if (visited.has(obj)) return;
    visited.add(obj);
    if (Object.prototype.hasOwnProperty.call(obj, 'workspaceScopedServices')) { bag = obj; return; }
    var ks;
    try { ks = Object.keys(obj); } catch (e) { return; }
    if (ks.length > 200) return;
    for (var i = 0; i < ks.length; i++) {
      var v;
      try { v = obj[ks[i]]; } catch (e) { continue; }
      if (isObj(v)) deepFind(v, depth + 1);
    }
  }
  function walkFiber(fiber) {
    var f = fiber;
    while (f) {
      if (isObj(f.memoizedProps)) deepFind(f.memoizedProps, 0);
      if (isObj(f.pendingProps)) deepFind(f.pendingProps, 0);
      if (f.child) walkFiber(f.child);
      f = f.sibling;
    }
  }
  for (var ri = 0; ri < roots.length; ri++) { walkFiber(roots[ri]); if (bag) break; }
  if (!bag) {
    var err = new Error('no active workspace found; open a workspace in ZCode first');
    err.name = 'NoWorkspace';
    throw err;
  }
  return bag;
}
function __zClientId() {
  var v = null;
  try { v = localStorage.getItem('zcode-v4-client-id:v1'); } catch (e) {}
  if (!v) {
    var e2 = new Error('conversation client id unavailable');
    e2.name = 'NoClientId';
    throw e2;
  }
  return v;
}
`

// chatServicePrefix sets up the workspace bag, services, and workspace identity
// for an async service call body.
const chatServicePrefix = `
  var bag = __zFindBag();
  var svc = bag.workspaceScopedServices;
  var __ws = { workspacePath: bag.activeWorkspacePath };
  if (bag.workspaceIdentity) __ws.workspaceIdentity = bag.workspaceIdentity;
  if (bag.workspaceRemoteSessionId) __ws.remoteSessionId = bag.workspaceRemoteSessionId;
  if (!svc || !svc.zcodeSessionService || !svc.zcodeTaskService || !svc.zcodeAgentService) {
    var e0 = new Error('workspace chat services are unavailable');
    e0.name = 'NoChatServices';
    throw e0;
  }
`

type chatSession struct {
	SessionID   string `json:"sessionId"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	Mode        string `json:"mode"`
	SessionKind string `json:"sessionKind"`
	TitleSource string `json:"titleSource"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	TraceID     string `json:"traceId"`
}

type messagePart struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

type chatMessage struct {
	ID         string        `json:"id"`
	Role       string        `json:"role"`
	Content    string        `json:"content"`
	Thought    string        `json:"thought"`
	Timestamp  int64         `json:"timestamp"`
	DurationMs int64         `json:"durationMs"`
	Model      string        `json:"model"`
	TurnIndex  int           `json:"turnIndex"`
	Parts      []messagePart `json:"parts"`
}

type taskSnapshot struct {
	Meta struct {
		TaskID    string `json:"taskId"`
		Title     string `json:"title"`
		Status    string `json:"status"`
		Mode      string `json:"mode"`
		Model     string `json:"model"`
		Provider  string `json:"provider"`
		CreatedAt int64  `json:"createdAt"`
		UpdatedAt int64  `json:"updatedAt"`
		TraceID   string `json:"traceId"`
		Workspace string `json:"workspacePath"`
	} `json:"meta"`
	Runtime struct {
		PendingPermissions []json.RawMessage `json:"pendingPermissions"`
		PendingCommands    []json.RawMessage `json:"pendingCommands"`
	} `json:"runtime"`
	Messages []chatMessage `json:"messages"`
}

type commandAck struct {
	CommandID  string `json:"commandId"`
	Status     string `json:"status"`
	ReasonCode string `json:"reasonCode"`
	Message    string `json:"message"`
	Result     struct {
		Type      string `json:"type"`
		SessionID string `json:"sessionId"`
		Delivery  string `json:"delivery"`
		InputID   string `json:"inputId"`
		MessageID string `json:"messageId"`
	} `json:"result"`
}

func chatCommand(rest []string, opts globalOptions) error {
	if len(rest) == 0 {
		return errors.New("usage: zcodecli chat list|show|new|send")
	}
	switch rest[0] {
	case "list":
		return chatListCommand(opts)
	case "show":
		id, all, err := parseChatShowArgs(rest[1:])
		if err != nil {
			return err
		}
		return chatShowCommand(id, all, opts)
	case "new":
		prompt, err := parsePromptArg(rest[1:], "usage: zcodecli chat new \"<prompt>\"")
		if err != nil {
			return err
		}
		return chatNewCommand(prompt, opts)
	case "send":
		id, prompt, err := parseChatSendArgs(rest[1:])
		if err != nil {
			return err
		}
		return chatSendCommand(id, prompt, opts)
	default:
		return fmt.Errorf("unknown chat action %q", rest[0])
	}
}

func parseChatShowArgs(args []string) (string, bool, error) {
	var id string
	all := false
	for _, a := range args {
		switch {
		case a == "--all":
			all = true
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("unknown flag %q", a)
		case id == "":
			id = a
		default:
			return "", false, errors.New("usage: zcodecli chat show <sessionId> [--all]")
		}
	}
	if id == "" {
		return "", false, errors.New("usage: zcodecli chat show <sessionId> [--all]")
	}
	return id, all, nil
}

func parseChatSendArgs(args []string) (string, string, error) {
	var id, prompt string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return "", "", fmt.Errorf("unknown flag %q", a)
		}
		if id == "" {
			id = a
		} else if prompt == "" {
			prompt = a
		} else {
			return "", "", errors.New("usage: zcodecli chat send <sessionId> \"<prompt>\"")
		}
	}
	if id == "" || prompt == "" {
		return "", "", errors.New("usage: zcodecli chat send <sessionId> \"<prompt>\"")
	}
	return id, prompt, nil
}

func parsePromptArg(args []string, usage string) (string, error) {
	var prompt string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return "", fmt.Errorf("unknown flag %q", a)
		}
		if prompt == "" {
			prompt = a
		} else {
			return "", errors.New(usage)
		}
	}
	if prompt == "" {
		return "", errors.New(usage)
	}
	return prompt, nil
}

// evalChatService runs an async body (which may reference svc/__ws) inside the
// renderer and returns the JSON result.
func (s *bridgeSession) evalChatService(body string, timeout time.Duration) (json.RawMessage, error) {
	expr := chatPrelude + "\n(async () => {\n" + chatServicePrefix + "\n" + body + "\n})()"
	return s.evaluateTimeout(expr, timeout)
}

func jsLiteral(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func (s *bridgeSession) listChatSessions() ([]chatSession, error) {
	body := `
  var args = { workspacePath: __ws.workspacePath, includeArchived: false, limit: 50 };
  if (__ws.workspaceIdentity) args.workspaceIdentity = __ws.workspaceIdentity;
  if (__ws.remoteSessionId) args.remoteSessionId = __ws.remoteSessionId;
  var list = await svc.zcodeSessionService.listSessions(args);
  return list;
`
	raw, err := s.evalChatService(body, 30*time.Second)
	if err != nil {
		return nil, err
	}
	var sessions []chatSession
	if err := json.Unmarshal(raw, &sessions); err != nil {
		return nil, contractMismatch("chat list: expected an array: %w", err)
	}
	for i, sess := range sessions {
		if sess.SessionID == "" {
			return nil, contractMismatch("chat list: session %d has an empty sessionId", i)
		}
	}
	return sessions, nil
}

func (s *bridgeSession) fetchTaskSnapshot(sessionID string, messageLimit int) (*taskSnapshot, error) {
	body := `
  var args = { taskId: ` + jsLiteral(sessionID) + `, workspacePath: __ws.workspacePath };
  if (__ws.workspaceIdentity) args.workspaceIdentity = __ws.workspaceIdentity;
  if (__ws.remoteSessionId) args.remoteSessionId = __ws.remoteSessionId;
  if (` + fmt.Sprintf("%d", messageLimit) + ` > 0) args.messageLimit = ` + fmt.Sprintf("%d", messageLimit) + `;
  var snap = await svc.zcodeTaskService.getTaskSnapshot(args);
  return snap;
`
	raw, err := s.evalChatService(body, 45*time.Second)
	if err != nil {
		return nil, err
	}
	var snap taskSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, contractMismatch("task snapshot: %w", err)
	}
	if snap.Meta.TaskID == "" {
		return nil, contractMismatch("task snapshot: missing meta.taskId")
	}
	return &snap, nil
}

func (s *bridgeSession) sendCommand(envelopeType string, sessionID *string, payload map[string]any) (*commandAck, error) {
	cmdID := newUUID()
	issuedAt := time.Now().UnixMilli()
	sessionLit := "null"
	if sessionID != nil {
		sessionLit = jsLiteral(*sessionID)
	}
	body := `
  var envelope = {
    commandId: ` + jsLiteral(cmdID) + `,
    clientId: __zClientId(),
    sessionId: ` + sessionLit + `,
    type: ` + jsLiteral(envelopeType) + `,
    payload: ` + jsLiteral(payload) + `,
    issuedAt: ` + fmt.Sprintf("%d", issuedAt) + `
  };
  var req = { workspacePath: __ws.workspacePath, envelope: envelope };
  if (__ws.workspaceIdentity) req.workspaceIdentity = __ws.workspaceIdentity;
  var ack = await svc.zcodeAgentService.sendConversationCommandV4(req);
  return ack;
`
	raw, err := s.evalChatService(body, 60*time.Second)
	if err != nil {
		return nil, err
	}
	var ack commandAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return nil, contractMismatch("command ack: %w", err)
	}
	return &ack, nil
}

func (s *bridgeSession) createSession() (string, *commandAck, error) {
	// workspaceId mirrors the UI: identity (trimmed) when present, else path.
	// No config/model is supplied so the workspace's real defaults are used.
	body := `
  var workspaceId = (__ws.workspaceIdentity && String(__ws.workspaceIdentity).trim()) || __ws.workspacePath;
  var envelope = {
    commandId: ` + jsLiteral(newUUID()) + `,
    clientId: __zClientId(),
    sessionId: null,
    type: 'createSession',
    payload: { workspaceId: workspaceId },
    issuedAt: ` + fmt.Sprintf("%d", time.Now().UnixMilli()) + `
  };
  var req = { workspacePath: __ws.workspacePath, envelope: envelope };
  if (__ws.workspaceIdentity) req.workspaceIdentity = __ws.workspaceIdentity;
  var ack = await svc.zcodeAgentService.sendConversationCommandV4(req);
  return ack;
`
	raw, err := s.evalChatService(body, 60*time.Second)
	if err != nil {
		return "", nil, err
	}
	var ack commandAck
	if err := json.Unmarshal(raw, &ack); err != nil {
		return "", nil, contractMismatch("create session ack: %w", err)
	}
	if ack.Status != "accepted" {
		return "", &ack, fmt.Errorf("create session rejected: %s", ackReason(ack))
	}
	if ack.Result.Type != "createSession" || ack.Result.SessionID == "" {
		return "", &ack, contractMismatch("create session: accepted but no sessionId returned")
	}
	return ack.Result.SessionID, &ack, nil
}

func ackReason(ack commandAck) string {
	parts := []string{ack.Status}
	if ack.ReasonCode != "" {
		parts = append(parts, ack.ReasonCode)
	}
	if ack.Message != "" {
		parts = append(parts, ack.Message)
	}
	return strings.Join(parts, ": ")
}

func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand should never fail on supported platforms; fall back anyway.
		return fmt.Sprintf("zcodecli-%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func openChatSession(port int) (*bridgeSession, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return openBridgeSession(ctx, port)
}

func chatListCommand(opts globalOptions) error {
	s, err := openChatSession(opts.port)
	if err != nil {
		return err
	}
	defer s.Close()
	sessions, err := s.listChatSessions()
	if err != nil {
		return err
	}
	if opts.json {
		return printJSON(sessions)
	}
	if len(sessions) == 0 {
		fmt.Println("No chat sessions found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION ID\tTITLE\tSTATUS\tMODE\tKIND\tUPDATED")
	for _, sess := range sessions {
		title := sess.Title
		if title == "" {
			title = "(untitled)"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			sess.SessionID, title, dash(sess.Status), dash(sess.Mode), dash(sess.SessionKind), formatMillis(sess.UpdatedAt))
	}
	_ = w.Flush()
	return nil
}

func chatShowCommand(id string, all bool, opts globalOptions) error {
	s, err := openChatSession(opts.port)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := requireSessionExists(s, id); err != nil {
		return err
	}
	limit := chatDefaultMessageLimit
	if all {
		limit = 0
	}
	snap, err := s.fetchTaskSnapshot(id, limit)
	if err != nil {
		return err
	}
	if opts.json {
		return printJSON(buildSnapshotView(snap))
	}
	printSnapshotHuman(snap, limit)
	return nil
}

func chatNewCommand(prompt string, opts globalOptions) error {
	s, err := openChatSession(opts.port)
	if err != nil {
		return err
	}
	defer s.Close()

	sessionID, _, err := s.createSession()
	if err != nil {
		return err
	}
	if err := sendPrompt(s, sessionID, prompt); err != nil {
		return err
	}
	reply, turn, err := waitForReply(s, sessionID, prompt, -1)
	if err != nil {
		return fmt.Errorf("session %s: %w", sessionID, err)
	}
	if opts.json {
		return printJSON(map[string]any{
			"sessionId": sessionID,
			"turnIndex": turn,
			"status":    "completed",
			"reply":     reply,
		})
	}
	fmt.Printf("Session: %s\n\n", sessionID)
	fmt.Println(reply)
	return nil
}

func chatSendCommand(id, prompt string, opts globalOptions) error {
	s, err := openChatSession(opts.port)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := requireSessionExists(s, id); err != nil {
		return err
	}
	base, err := s.fetchTaskSnapshot(id, 0)
	if err != nil {
		return err
	}
	baselineTurn := maxTurnIndex(base.Messages)
	if err := sendPrompt(s, id, prompt); err != nil {
		return err
	}
	reply, turn, err := waitForReply(s, id, prompt, baselineTurn)
	if err != nil {
		return fmt.Errorf("session %s: %w", id, err)
	}
	if opts.json {
		return printJSON(map[string]any{
			"sessionId": id,
			"turnIndex": turn,
			"status":    "completed",
			"reply":     reply,
		})
	}
	fmt.Printf("Session: %s\n\n", id)
	fmt.Println(reply)
	return nil
}

func sendPrompt(s *bridgeSession, sessionID, prompt string) error {
	ack, err := s.sendCommand("sendText", &sessionID, map[string]any{"text": prompt})
	if err != nil {
		return err
	}
	if ack.Status != "accepted" {
		return fmt.Errorf("send rejected: %s", ackReason(*ack))
	}
	return nil
}

func requireSessionExists(s *bridgeSession, id string) error {
	sessions, err := s.listChatSessions()
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.SessionID == id {
			return nil
		}
	}
	return fmt.Errorf("session %q was not found in the active workspace; use `zcodecli chat list` to see valid ids", id)
}

// waitForReply polls the task snapshot until the agent finishes the turn that
// contains our prompt. baselineTurn is the max turnIndex before sending (-1 for
// a brand-new session).
func waitForReply(s *bridgeSession, sessionID, prompt string, baselineTurn int) (string, int, error) {
	targetTurn := baselineTurn + 1
	deadline := time.Now().Add(chatReplyTimeout)
	want := strings.TrimSpace(prompt)
	lastStatus := ""
	for time.Now().Before(deadline) {
		snap, err := s.fetchTaskSnapshot(sessionID, 30)
		if err == nil {
			lastStatus = snap.Meta.Status
			if snap.Meta.Status == "error" {
				return "", 0, errors.New("agent reported an error while running the turn")
			}
			if len(snap.Runtime.PendingPermissions) > 0 {
				return "", 0, errors.New("agent is waiting for a permission or approval that zcodecli cannot grant; open ZCode to continue this session")
			}
			turn := -1
			for i := range snap.Messages {
				m := snap.Messages[i]
				if m.Role == "user" && m.TurnIndex >= targetTurn && strings.TrimSpace(m.Content) == want {
					turn = m.TurnIndex
				}
			}
			if turn >= 0 {
				for i := range snap.Messages {
					m := snap.Messages[i]
					if m.Role == "assistant" && m.TurnIndex == turn {
						text := assistantVisibleText(m)
						if text != "" && !isRunningStatus(snap.Meta.Status) {
							return text, turn, nil
						}
					}
				}
			}
		}
		time.Sleep(chatPollInterval)
	}
	return "", 0, fmt.Errorf("timed out after %s waiting for the agent reply (last status %q)", chatReplyTimeout, lastStatus)
}

func isRunningStatus(status string) bool {
	switch status {
	case "running", "paused":
		return true
	default:
		return false
	}
}

func maxTurnIndex(msgs []chatMessage) int {
	max := -1
	for _, m := range msgs {
		if m.TurnIndex > max {
			max = m.TurnIndex
		}
	}
	return max
}

// assistantVisibleText returns the user-visible reply text. content is the
// merged visible text; fall back to concatenating content parts (thoughts and
// tool calls are never surfaced).
func assistantVisibleText(m chatMessage) string {
	if strings.TrimSpace(m.Content) != "" {
		return strings.TrimSpace(m.Content)
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Type == "content" {
			b.WriteString(p.Content)
		}
	}
	return strings.TrimSpace(b.String())
}

func dash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func formatMillis(ms int64) string {
	if ms <= 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04:05")
}

type chatMessageView struct {
	Role       string `json:"role"`
	TurnIndex  int    `json:"turnIndex"`
	Content    string `json:"content"`
	Timestamp  int64  `json:"timestamp"`
	Model      string `json:"model,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

type snapshotView struct {
	SessionID string            `json:"sessionId"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Mode      string            `json:"mode"`
	Model     string            `json:"model"`
	Provider  string            `json:"provider"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
	Messages  []chatMessageView `json:"messages"`
}

func buildSnapshotView(snap *taskSnapshot) snapshotView {
	v := snapshotView{
		SessionID: snap.Meta.TaskID,
		Title:     snap.Meta.Title,
		Status:    snap.Meta.Status,
		Mode:      snap.Meta.Mode,
		Model:     snap.Meta.Model,
		Provider:  snap.Meta.Provider,
		CreatedAt: snap.Meta.CreatedAt,
		UpdatedAt: snap.Meta.UpdatedAt,
		Messages:  make([]chatMessageView, 0, len(snap.Messages)),
	}
	for _, m := range snap.Messages {
		content := m.Content
		if m.Role == "assistant" {
			content = assistantVisibleText(m)
		}
		v.Messages = append(v.Messages, chatMessageView{
			Role:       m.Role,
			TurnIndex:  m.TurnIndex,
			Content:    content,
			Timestamp:  m.Timestamp,
			Model:      m.Model,
			DurationMs: m.DurationMs,
		})
	}
	return v
}

func printSnapshotHuman(snap *taskSnapshot, limit int) {
	v := buildSnapshotView(snap)
	fmt.Printf("Session:  %s\n", v.SessionID)
	fmt.Printf("Title:    %s\n", dash(v.Title))
	fmt.Printf("Status:   %s\n", dash(v.Status))
	fmt.Printf("Model:    %s\n", dash(v.Model))
	fmt.Printf("Updated:  %s\n", formatMillis(v.UpdatedAt))
	if limit > 0 && len(v.Messages) >= limit {
		fmt.Printf("Showing:  most recent %d messages (use --all for full history)\n", limit)
	}
	fmt.Println()
	for _, m := range v.Messages {
		who := "you"
		if m.Role == "assistant" {
			who = "agent"
		}
		fmt.Printf("── turn %d · %s · %s ──\n", m.TurnIndex, who, formatMillis(m.Timestamp))
		text := m.Content
		if strings.TrimSpace(text) == "" {
			text = "(no text content)"
		}
		fmt.Println(text)
		fmt.Println()
	}
}
