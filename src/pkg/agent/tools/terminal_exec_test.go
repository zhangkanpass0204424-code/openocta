package tools

import (
	"context"
	"testing"
)

type terminalExecutorStub struct {
	sessionID string
	command   string
	output    string
}

func (s *terminalExecutorStub) ExecuteCommand(_ context.Context, sessionID, command string) (string, error) {
	s.sessionID, s.command = sessionID, command
	if s.output != "" {
		return s.output, nil
	}
	return `{"completed":true,"exitCode":0,"output":"ok"}`, nil
}

func TestTerminalExecToolRejectsInvalidCompletion(t *testing.T) {
	for _, output := range []string{"plain output", `{}`, `{"completed":true}`, `{"completed":false,"exitCode":0}`, `{"completed":true,"exitCode":7}`} {
		t.Run(output, func(t *testing.T) {
			stub := &terminalExecutorStub{output: output}
			result, err := (TerminalExecTool{Executor: stub, Allow: true, SessionID: "bound"}).Execute(context.Background(), map[string]interface{}{"command": "pwd"})
			if err != nil || result.Success {
				t.Fatalf("invalid or unsuccessful execution reported success: %+v %v", result, err)
			}
		})
	}
}

func TestTerminalExecToolRequiresArguments(t *testing.T) {
	tool := TerminalExecTool{Executor: &terminalExecutorStub{}, Allow: true, SessionID: "session-1"}
	result, err := tool.Execute(context.Background(), map[string]interface{}{"command": ""})
	if err != nil || result.Success || result.Output != "sessionId and command are required" {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
}

func TestTerminalExecToolUsesExistingSession(t *testing.T) {
	stub := &terminalExecutorStub{}
	result, err := (TerminalExecTool{Executor: stub, Allow: true, SessionID: "session-1"}).Execute(context.Background(), map[string]interface{}{
		"sessionId": "session-1",
		"command":   "pwd",
	})
	if err != nil || !result.Success || result.Output != `{"completed":true,"exitCode":0,"output":"ok"}` {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
	if stub.sessionID != "session-1" || stub.command != "pwd" {
		t.Fatalf("executor received (%q, %q)", stub.sessionID, stub.command)
	}
}

func TestTerminalExecToolRejectsMultilineCommand(t *testing.T) {
	result, err := (TerminalExecTool{Executor: &terminalExecutorStub{}, Allow: true, SessionID: "session-1"}).Execute(context.Background(), map[string]interface{}{
		"sessionId": "session-1",
		"command":   "echo one\necho two",
	})
	if err != nil || result.Success || result.Output != "command must be one line and cannot contain control characters" {
		t.Fatalf("unexpected result: %#v, %v", result, err)
	}
}

func TestTerminalExecToolRejectsDifferentSession(t *testing.T) {
	stub := &terminalExecutorStub{}
	result, _ := (TerminalExecTool{Executor: stub, Allow: true, SessionID: "bound"}).Execute(context.Background(), map[string]interface{}{"sessionId": "other", "command": "pwd"})
	if result.Success || stub.command != "" {
		t.Fatal("wrong target executed")
	}
}

func TestTerminalExecToolUsesBoundSessionWithoutModelTarget(t *testing.T) {
	stub := &terminalExecutorStub{}
	result, _ := (TerminalExecTool{Executor: stub, Allow: true, SessionID: "bound"}).Execute(context.Background(), map[string]interface{}{"command": "pwd"})
	if !result.Success || stub.sessionID != "bound" {
		t.Fatal("bound target not used")
	}
}

func TestTerminalExecToolRejectsUnboundTarget(t *testing.T) {
	stub := &terminalExecutorStub{}
	result, _ := (TerminalExecTool{Executor: stub, Allow: true}).Execute(context.Background(), map[string]interface{}{"sessionId": "other", "command": "pwd"})
	if result.Success || stub.command != "" {
		t.Fatal("unbound execution allowed")
	}
}
