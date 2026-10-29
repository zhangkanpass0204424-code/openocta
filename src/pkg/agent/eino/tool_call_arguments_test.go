package eino

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/openocta/openocta/pkg/agent/types"
	"github.com/openocta/openocta/pkg/session"
)

func TestNormalizeToolCallArgumentsJSON(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "{}"},
		{"whitespace", "  ", "{}"},
		{"json empty string", `""`, "{}"},
		{"json null", "null", "{}"},
		{"object", `{"command":"ipconfig"}`, `{"command":"ipconfig"}`},
		{"double encoded object", `"{\"command\":\"ipconfig\"}"`, `{"command":"ipconfig"}`},
		{"invalid", "not-json", "{}"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeToolCallArgumentsJSON(tc.in)
			if got != tc.want {
				t.Fatalf("NormalizeToolCallArgumentsJSON(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if !json.Valid([]byte(got)) || got[0] != '{' {
				t.Fatalf("expected JSON object, got %q", got)
			}
		})
	}
}

func TestSchemaMessagesFromTranscriptEmptyToolArguments(t *testing.T) {
	t.Parallel()
	msgs := []session.TranscriptMessage{
		{
			Role: "assistant",
			Content: []session.ContentBlock{
				{Type: "text", Text: "running"},
				{Type: "toolCall", ID: "execute_0", Name: "execute", Arguments: json.RawMessage(`""`)},
			},
		},
	}
	out := SchemaMessagesFromTranscript(msgs, TranscriptLoadOptions{})
	if len(out) != 1 || len(out[0].ToolCalls) != 1 {
		t.Fatalf("unexpected messages: %#v", out)
	}
	if out[0].ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("expected {}, got %q", out[0].ToolCalls[0].Function.Arguments)
	}
}

func TestRepairIncompleteToolCallMessages(t *testing.T) {
	t.Parallel()
	msgs := []*schema.Message{
		schema.UserMessage("今天天气怎么样"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:   "call_00_sc59kLV9nO5z1oa2XuCU3366",
			Type: "function",
			Function: schema.FunctionCall{
				Name:      "memory_search",
				Arguments: "{}",
			},
		}}),
		schema.UserMessage("我在杭州"),
	}
	out := repairIncompleteToolCallMessages(msgs)
	if len(out) != 4 {
		t.Fatalf("expected 4 messages, got %d: %#v", len(out), out)
	}
	if out[2].Role != schema.Tool || out[2].ToolCallID != "call_00_sc59kLV9nO5z1oa2XuCU3366" {
		t.Fatalf("expected placeholder tool message, got %#v", out[2])
	}
	if out[3].Role != schema.User || out[3].Content != "我在杭州" {
		t.Fatalf("expected user message last, got %#v", out[3])
	}
}

// A streaming tool reports its output in several chunks that share one
// tool_call id. The provider rejects a request that answers one id twice
// (HTTP 400 "invalid params"), so the chunks must be merged into one result.
func TestRepairIncompleteToolCallMessagesMergesDuplicateIDs(t *testing.T) {
	t.Parallel()
	msgs := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "execute", Arguments: "{}"}},
			{ID: "call_b", Type: "function", Function: schema.FunctionCall{Name: "execute", Arguments: "{}"}},
		}),
		schema.ToolMessage("chunk one", "call_a", schema.WithToolName("execute")),
		schema.ToolMessage("chunk two", "call_a", schema.WithToolName("execute")),
		schema.ToolMessage("other call", "call_b", schema.WithToolName("execute")),
	}
	out := repairIncompleteToolCallMessages(msgs)

	counts := map[string]int{}
	for _, m := range out {
		if m.Role == schema.Tool {
			counts[m.ToolCallID]++
		}
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("tool_call %q answered %d times, want 1: %#v", id, n, out)
		}
	}
	if len(counts) != 2 {
		t.Fatalf("expected 2 tool results, got %d: %#v", len(counts), out)
	}
	// The merged result keeps every chunk instead of dropping the extra one.
	var merged string
	for _, m := range out {
		if m.Role == schema.Tool && m.ToolCallID == "call_a" {
			merged = m.Content
		}
	}
	if merged != "chunk onechunk two" {
		t.Fatalf("expected merged content %q, got %q", "chunk onechunk two", merged)
	}
}

// A tool result whose id was never declared is an orphan and must be dropped,
// otherwise the provider rejects the request with HTTP 400.
func TestRepairIncompleteToolCallMessagesDropsOrphanResult(t *testing.T) {
	t.Parallel()
	msgs := []*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "execute", Arguments: "{}"}},
		}),
		schema.ToolMessage("orphan", "call_unknown", schema.WithToolName("execute")),
	}
	out := repairIncompleteToolCallMessages(msgs)
	for _, m := range out {
		if m.Role == schema.Tool && m.ToolCallID == "call_unknown" {
			t.Fatalf("expected orphan tool result to be dropped: %#v", out)
		}
	}
}

// Every declared tool call must be answered exactly once: a result may not be
// missing (the provider answers 500) and no id may be answered twice (400).
func TestPrepareSchemaMessagesForModelKeepsToolPairingOneToOne(t *testing.T) {
	t.Parallel()
	out := PrepareSchemaMessagesForModel([]*schema.Message{
		schema.AssistantMessage("", []schema.ToolCall{
			{ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "execute", Arguments: "{}"}},
			{ID: "call_b", Type: "function", Function: schema.FunctionCall{Name: "execute", Arguments: "{}"}},
		}),
		schema.ToolMessage("first", "call_a"),
		schema.ToolMessage("dup", "call_a"),
	})
	declared := map[string]int{}
	answered := map[string]int{}
	for _, m := range out {
		switch m.Role {
		case schema.Assistant:
			for _, tc := range m.ToolCalls {
				declared[tc.ID]++
			}
		case schema.Tool:
			answered[m.ToolCallID]++
		}
	}
	for id := range declared {
		if answered[id] != 1 {
			t.Fatalf("tool_call %q answered %d times, want 1: %#v", id, answered[id], out)
		}
	}
	for id := range answered {
		if declared[id] == 0 {
			t.Fatalf("orphan tool result %q: %#v", id, out)
		}
	}
}

func TestBuildAgentMessagesRepairsInterruptedToolTurn(t *testing.T) {
	t.Parallel()
	msgs, err := BuildAgentMessages(types.Request{
		Prompt: "我在杭州",
		SessionMessages: []*schema.Message{
			schema.UserMessage("今天天气怎么样"),
			schema.AssistantMessage("", []schema.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "memory_search",
					Arguments: "{}",
				},
			}}),
			schema.UserMessage("我在杭州"),
		},
	})
	if err != nil {
		t.Fatalf("BuildAgentMessages: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d: %#v", len(msgs), msgs)
	}
	if msgs[2].Role != schema.Tool {
		t.Fatalf("expected repaired tool message at index 2, got %#v", msgs[2])
	}
}

func TestBuildAgentMessagesDoesNotDuplicateToolResultsAfterReorder(t *testing.T) {
	t.Parallel()
	msgs, err := BuildAgentMessages(types.Request{
		Prompt: "next",
		SessionMessages: []*schema.Message{
			schema.UserMessage("browse"),
			schema.ToolMessage(`{"ok":true}`, "call_a", schema.WithToolName("browser")),
			schema.ToolMessage("shot", "call_b", schema.WithToolName("browser")),
			schema.AssistantMessage("", []schema.ToolCall{
				{ID: "call_a", Type: "function", Function: schema.FunctionCall{Name: "browser", Arguments: "{}"}},
				{ID: "call_b", Type: "function", Function: schema.FunctionCall{Name: "browser", Arguments: "{}"}},
			}),
			schema.UserMessage("next"),
		},
	})
	if err != nil {
		t.Fatalf("BuildAgentMessages: %v", err)
	}
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d: %#v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.Role == schema.Tool && m.Content == interruptedToolResultText {
			t.Fatalf("unexpected interrupted placeholder: %#v", msgs)
		}
	}
	if msgs[2].Role != schema.Tool || msgs[3].Role != schema.Tool {
		t.Fatalf("expected tool results after assistant: %#v", msgs)
	}
}

func TestBuildAgentMessagesSanitizesToolCallArguments(t *testing.T) {
	t.Parallel()
	msgs, err := BuildAgentMessages(types.Request{
		SessionMessages: []*schema.Message{
			schema.AssistantMessage("ok", []schema.ToolCall{{
				ID:   "call_1",
				Type: "function",
				Function: schema.FunctionCall{
					Name:      "execute",
					Arguments: `""`,
				},
			}}),
			schema.ToolMessage("done", "call_1"),
		},
	})
	if err != nil {
		t.Fatalf("BuildAgentMessages: %v", err)
	}
	if len(msgs) != 2 || len(msgs[0].ToolCalls) != 1 {
		t.Fatalf("unexpected messages: %#v", msgs)
	}
	if msgs[0].ToolCalls[0].Function.Arguments != "{}" {
		t.Fatalf("expected {}, got %q", msgs[0].ToolCalls[0].Function.Arguments)
	}
}
