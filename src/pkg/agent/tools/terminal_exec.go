package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/openocta/openocta/pkg/agent/tool"
)

// TerminalExecutor is implemented by the Gateway SSH session manager.
type TerminalExecutor interface {
	ExecuteCommand(ctx context.Context, sessionID, command string) (string, error)
}

// TerminalExecTool executes a command in the user's currently selected SSH PTY.
type TerminalExecTool struct {
	Executor  TerminalExecutor
	Allow     bool
	SessionID string
}

func (TerminalExecTool) Name() string { return "terminal_exec" }

func (TerminalExecTool) Description() string {
	return "Execute a non-interactive shell command in the explicitly provided OpenOcta SSH terminal session. " +
		"The command is visible in the user's terminal and the resulting output is returned here. " +
		"Do not use for interactive programs such as vim, top, passwd, or commands that require a TTY prompt."
}

func (TerminalExecTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"sessionId": map[string]interface{}{"type": "string", "description": "The active OpenOcta SSH terminal session ID from the user context"},
			"command":   map[string]interface{}{"type": "string", "description": "One non-interactive command to execute"},
		},
		Required: []string{"command"},
	}
}

func (t TerminalExecTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if !t.Allow {
		return &tool.ToolResult{Success: false, Output: "terminal execution requires explicit user approval"}, nil
	}
	if t.SessionID == "" {
		return &tool.ToolResult{Success: false, Output: "terminal target is not bound; select a connected terminal"}, nil
	}
	if t.Executor == nil {
		return &tool.ToolResult{Success: false, Output: "terminal_exec is unavailable"}, nil
	}
	sessionID, _ := params["sessionId"].(string)
	command, _ := params["command"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if t.SessionID != "" {
		if sessionID != "" && sessionID != t.SessionID {
			return &tool.ToolResult{Success: false, Output: "terminal target mismatch; execution refused"}, nil
		}
		sessionID = t.SessionID
	}
	command = strings.TrimSpace(command)
	if sessionID == "" || command == "" {
		return &tool.ToolResult{Success: false, Output: "sessionId and command are required"}, nil
	}
	if len(command) > 16*1024 {
		return &tool.ToolResult{Success: false, Output: "command is too large"}, nil
	}
	if strings.ContainsAny(command, "\x00\r\n") || strings.IndexFunc(command, unicode.IsControl) >= 0 {
		return &tool.ToolResult{Success: false, Output: "command must be one line and cannot contain control characters"}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	output, err := t.Executor.ExecuteCommand(ctx, sessionID, command)
	if err != nil {
		return &tool.ToolResult{Success: false, Output: fmt.Sprintf("terminal command failed: %v\n%s", err, output)}, nil
	}
	var execution struct {
		Completed bool `json:"completed"`
		ExitCode  *int `json:"exitCode"`
	}
	if json.Unmarshal([]byte(output), &execution) == nil && execution.ExitCode != nil {
		return &tool.ToolResult{Success: execution.Completed && *execution.ExitCode == 0, Output: output}, nil
	}
	return &tool.ToolResult{Success: false, Output: "terminal execution result is missing a valid completion status and exit code: " + output}, nil
}
