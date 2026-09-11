// Unit tests for chat helpers: visible-text extraction, turn indexing,
// argument parsing, and ack reasons. No app or network required.

package main

import "testing"

func TestAssistantVisibleTextPrefersContent(t *testing.T) {
	m := chatMessage{
		Role:    "assistant",
		Content: "final answer",
		Parts:   []messagePart{{Type: "thought", Content: "secret"}, {Type: "content", Content: "ignored"}},
	}
	if got := assistantVisibleText(m); got != "final answer" {
		t.Fatalf("expected merged content, got %q", got)
	}
}

func TestAssistantVisibleTextFallsBackToContentParts(t *testing.T) {
	m := chatMessage{
		Role: "assistant",
		Parts: []messagePart{
			{Type: "thought", Content: "thinking"},
			{Type: "content", Content: "hello "},
			{Type: "tool-call", Content: "{}"},
			{Type: "content", Content: "world"},
		},
	}
	if got := assistantVisibleText(m); got != "hello world" {
		t.Fatalf("expected concatenated content parts, got %q", got)
	}
}

func TestAssistantVisibleTextIgnoresThoughtsOnly(t *testing.T) {
	m := chatMessage{Role: "assistant", Parts: []messagePart{{Type: "thought", Content: "hmm"}}}
	if got := assistantVisibleText(m); got != "" {
		t.Fatalf("expected empty visible text, got %q", got)
	}
}

func TestRunningStatusClassification(t *testing.T) {
	for _, running := range []string{"running", "paused"} {
		if !isRunningStatus(running) {
			t.Errorf("expected %q to be running", running)
		}
	}
	for _, done := range []string{"completed", "idle", "waiting", "error", ""} {
		if isRunningStatus(done) {
			t.Errorf("did not expect %q to be running", done)
		}
	}
}

func TestMaxTurnIndex(t *testing.T) {
	msgs := []chatMessage{
		{Role: "user", TurnIndex: 0},
		{Role: "assistant", TurnIndex: 0},
		{Role: "user", TurnIndex: 1},
		{Role: "assistant", TurnIndex: 1},
	}
	if got := maxTurnIndex(msgs); got != 1 {
		t.Fatalf("expected max turn 1, got %d", got)
	}
	if got := maxTurnIndex(nil); got != -1 {
		t.Fatalf("expected -1 for empty history, got %d", got)
	}
}

func TestParseChatShowArgs(t *testing.T) {
	id, all, err := parseChatShowArgs([]string{"sess_123"})
	if err != nil || id != "sess_123" || all {
		t.Fatalf("unexpected: %q %v %v", id, all, err)
	}
	id, all, err = parseChatShowArgs([]string{"sess_123", "--all"})
	if err != nil || id != "sess_123" || !all {
		t.Fatalf("unexpected: %q %v %v", id, all, err)
	}
	if _, _, err := parseChatShowArgs([]string{}); err == nil {
		t.Fatal("expected error when id missing")
	}
	if _, _, err := parseChatShowArgs([]string{"a", "b"}); err == nil {
		t.Fatal("expected error with extra positional")
	}
	if _, _, err := parseChatShowArgs([]string{"a", "--bogus"}); err == nil {
		t.Fatal("expected error on unknown flag")
	}
}

func TestParseChatSendArgs(t *testing.T) {
	id, prompt, err := parseChatSendArgs([]string{"sess_9", "hello world"})
	if err != nil || id != "sess_9" || prompt != "hello world" {
		t.Fatalf("unexpected: %q %q %v", id, prompt, err)
	}
	if _, _, err := parseChatSendArgs([]string{"sess_9"}); err == nil {
		t.Fatal("expected error when prompt missing")
	}
	if _, _, err := parseChatSendArgs([]string{}); err == nil {
		t.Fatal("expected error with no args")
	}
}

func TestParsePromptArg(t *testing.T) {
	prompt, err := parsePromptArg([]string{"reply ok"}, "usage")
	if err != nil || prompt != "reply ok" {
		t.Fatalf("unexpected: %q %v", prompt, err)
	}
	if _, err := parsePromptArg(nil, "usage"); err == nil {
		t.Fatal("expected error with empty prompt")
	}
}

func TestAckReason(t *testing.T) {
	ack := commandAck{Status: "rejected", ReasonCode: "bad", Message: "nope"}
	if got := ackReason(ack); got != "rejected: bad: nope" {
		t.Fatalf("unexpected reason %q", got)
	}
}
