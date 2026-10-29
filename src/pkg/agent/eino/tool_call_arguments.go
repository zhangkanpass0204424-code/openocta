package eino

import (
	"encoding/json"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// NormalizeToolCallArgumentsJSON ensures function.arguments is a JSON object string.
// Some providers reject empty strings, bare JSON strings, null, or double-encoded JSON.
func NormalizeToolCallArgumentsJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "{}"
	}
	if !json.Valid([]byte(raw)) {
		return "{}"
	}
	if strings.HasPrefix(raw, "{") {
		return raw
	}
	var inner string
	if err := json.Unmarshal([]byte(raw), &inner); err == nil {
		inner = strings.TrimSpace(inner)
		if inner != "" && json.Valid([]byte(inner)) && strings.HasPrefix(inner, "{") {
			return inner
		}
	}
	return "{}"
}

// NormalizeToolCallArgumentsRaw normalizes raw transcript/stream tool argument bytes.
func NormalizeToolCallArgumentsRaw(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(NormalizeToolCallArgumentsJSON(string(raw)))
}

func sanitizeSchemaMessagesToolCalls(msgs []*schema.Message) {
	for _, msg := range msgs {
		if msg == nil || msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}
		for i := range msg.ToolCalls {
			msg.ToolCalls[i].Function.Arguments = NormalizeToolCallArgumentsJSON(msg.ToolCalls[i].Function.Arguments)
		}
	}
}

// PrepareSchemaMessagesForModel normalizes transcript/agent history so OpenAI-compatible
// providers (DeepSeek, etc.) accept tool-call turns: arguments sanitized, tool results
// reordered after assistant tool_calls, and missing tool responses filled with placeholders.
func PrepareSchemaMessagesForModel(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := append([]*schema.Message(nil), msgs...)
	sanitizeSchemaMessagesToolCalls(out)
	out = normalizeToolTurnMessageOrder(out)
	out = repairIncompleteToolCallMessages(out)
	return out
}

const interruptedToolResultText = "[工具执行未完成或被中断]"

// repairIncompleteToolCallMessages rewrites tool-call turns so every assistant
// tool_call is answered by exactly one tool message carrying the same tool_call
// id, and no tool message references an unknown id.
//
// OpenAI-compatible providers (MiniMax, DeepSeek, ...) reject the whole request
// with HTTP 400 "invalid params" when a tool_call id is duplicated, orphaned or
// missing, which used to wedge a session after a single malformed tool turn.
// Tool results are re-emitted next to the assistant turn that declared them;
// results sharing an id are merged, orphans are dropped and missing results get
// a placeholder.
func repairIncompleteToolCallMessages(msgs []*schema.Message) []*schema.Message {
	if len(msgs) == 0 {
		return msgs
	}
	// Index tool results by tool_call id. A streaming tool reports its output in
	// several chunks that share one id, so the chunks are merged into a single
	// tool message instead of being kept as separate rows.
	results := make(map[string]*schema.Message)
	for _, msg := range msgs {
		if msg == nil || msg.Role != schema.Tool {
			continue
		}
		id := strings.TrimSpace(msg.ToolCallID)
		if id == "" {
			continue
		}
		if prev, ok := results[id]; ok {
			results[id] = mergeToolMessages(prev, msg)
			continue
		}
		results[id] = msg
	}

	out := make([]*schema.Message, 0, len(msgs)+4)
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if msg.Role == schema.Tool {
			// Tool results are re-emitted next to the assistant turn that declared
			// them; a tool message that matches no declared call is an orphan.
			continue
		}
		out = append(out, msg)
		if msg.Role != schema.Assistant || len(msg.ToolCalls) == 0 {
			continue
		}
		for _, tc := range msg.ToolCalls {
			id := strings.TrimSpace(tc.ID)
			if id == "" {
				continue
			}
			if res, ok := results[id]; ok {
				out = append(out, res)
				continue
			}
			opts := []schema.ToolMessageOption{}
			if name := strings.TrimSpace(tc.Function.Name); name != "" {
				opts = append(opts, schema.WithToolName(name))
			}
			out = append(out, schema.ToolMessage(interruptedToolResultText, id, opts...))
		}
	}
	return out
}

// mergeToolMessages concatenates two tool results that share a tool_call id
// into a single message, so no output is lost while the id stays unique.
func mergeToolMessages(prev, next *schema.Message) *schema.Message {
	if prev == nil {
		return next
	}
	if next == nil {
		return prev
	}
	merged := *prev
	if strings.TrimSpace(next.Content) != "" {
		if strings.TrimSpace(merged.Content) == "" {
			merged.Content = next.Content
		} else {
			merged.Content += next.Content
		}
	}
	merged.MultiContent = append(append([]schema.ChatMessagePart(nil), prev.MultiContent...), next.MultiContent...)
	merged.UserInputMultiContent = append(append([]schema.MessageInputPart(nil), prev.UserInputMultiContent...), next.UserInputMultiContent...)
	if strings.TrimSpace(merged.Name) == "" {
		merged.Name = next.Name
	}
	if strings.TrimSpace(merged.ToolName) == "" {
		merged.ToolName = next.ToolName
	}
	return &merged
}
