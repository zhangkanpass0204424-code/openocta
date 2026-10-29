package eino

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	einomodel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/openocta/openocta/pkg/agent/tool"
	"github.com/openocta/openocta/pkg/agent/tools"
	"github.com/openocta/openocta/pkg/agent/types"
)

type terminalLoopModel struct{ bound []*schema.ToolInfo }

func (m *terminalLoopModel) WithTools(ts []*schema.ToolInfo) (einomodel.ToolCallingChatModel, error) {
	return &terminalLoopModel{bound: ts}, nil
}
func (m *terminalLoopModel) Generate(_ context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.Message, error) {
	for _, msg := range input {
		if msg.Role == schema.Tool && msg.ToolName == "terminal_exec" {
			if !strings.Contains(msg.Content, "/tmp") {
				return nil, fmt.Errorf("tool result missing: %s", msg.Content)
			}
			return &schema.Message{Role: schema.Assistant, Content: "verified /tmp"}, nil
		}
	}
	found := false
	for _, t := range append(m.bound, einomodel.GetCommonOptions(&einomodel.Options{}, opts...).Tools...) {
		if t.Name == "terminal_exec" {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("terminal_exec was hidden behind tool search")
	}
	return &schema.Message{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{ID: "terminal-call", Type: "function", Function: schema.FunctionCall{Name: "terminal_exec", Arguments: `{"command":"pwd"}`}}}}, nil
}
func (m *terminalLoopModel) Stream(ctx context.Context, input []*schema.Message, opts ...einomodel.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

type loopExecutor struct{ called bool }

func (e *loopExecutor) ExecuteCommand(_ context.Context, id, cmd string) (string, error) {
	if id != "bound-session" || cmd != "pwd" {
		return "", fmt.Errorf("wrong target")
	}
	e.called = true
	return `{"completed":true,"exitCode":0,"output":"/tmp","sessionId":"bound-session"}`, nil
}

func TestTerminalToolVisibleAndResultReturnsToModel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	executor := &loopExecutor{}
	engine, err := NewEngine(ctx, BuildConfig{ProjectRoot: t.TempDir(), ModelFactory: ChatModelFactoryFunc(func(context.Context) (einomodel.ToolCallingChatModel, error) { return &terminalLoopModel{}, nil }), Tools: []tool.Tool{tools.EchoTool{}, tools.TerminalExecTool{Executor: executor, Allow: true, SessionID: "bound-session"}}, Env: func(key string) string {
		if key == "OPENOCTA_ADK_TOOLSEARCH" {
			return "true"
		}
		return ""
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprintf("streaming=%v", streaming), func(t *testing.T) {
			executor.called = false
			req := types.Request{Prompt: "Execute pwd in current terminal", SessionID: fmt.Sprintf("terminal-integration-%v", streaming)}
			var output string
			if streaming {
				events, err := engine.RunStream(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				for event := range events {
					if event.Type == "error" {
						t.Fatalf("stream error: %+v", event)
					}
					if event.Delta != nil {
						output += event.Delta.Text
					}
				}
			} else {
				result, err := engine.Run(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if result.Result != nil {
					output = result.Result.Output
				}
			}
			if !executor.called || !strings.Contains(output, "verified /tmp") {
				t.Fatalf("tool/model loop failed: called=%v output=%q", executor.called, output)
			}
		})
	}
}
