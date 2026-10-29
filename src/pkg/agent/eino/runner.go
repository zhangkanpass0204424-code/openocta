package eino

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"

	"github.com/openocta/openocta/pkg/agent/stream"
	"github.com/openocta/openocta/pkg/agent/types"
)

// Run executes a single non-streaming request.
func (e *Engine) Run(ctx context.Context, req types.Request) (*types.Response, error) {
	if e == nil || e.runner == nil {
		return nil, ErrRuntimeClosed
	}
	ctx = wrapRunContext(ctx, e.agentRunBudget)
	msgs, err := BuildAgentMessages(req)
	if err != nil {
		return nil, err
	}
	opts := e.runOptions(req)
	iter := e.runner.Run(ctx, msgs, opts...)
	var textParts []string
	for {
		evt, ok := iter.Next()
		if !ok {
			break
		}
		if evt == nil {
			continue
		}
		if evt.Err != nil {
			return nil, evt.Err
		}
		msg, _, err := adk.GetMessage(evt)
		if err != nil {
			return nil, err
		}
		if msg != nil && msg.Role == "assistant" && msg.Content != "" {
			textParts = append(textParts, msg.Content)
		}
	}
	out := strings.Join(textParts, "")
	return &types.Response{
		RequestID: req.RequestID,
		Result: &types.Result{
			Output:     out,
			StopReason: "end_turn",
		},
	}, nil
}

// RunStream executes with streaming events compatible with Gateway handlers.
func (e *Engine) RunStream(ctx context.Context, req types.Request) (<-chan stream.StreamEvent, error) {
	if e == nil || e.runner == nil {
		return nil, ErrRuntimeClosed
	}
	msgs, err := BuildAgentMessages(req)
	if err != nil {
		return nil, err
	}
	sessionID := strings.TrimSpace(req.SessionID)
	opts := e.runOptions(req)
	iter := e.runner.Run(ctx, msgs, opts...)
	return StreamEventsFromIterator(ctx, sessionID, strings.TrimSpace(req.RequestID), iter), nil
}

// ResumeStream continues an interrupted run with approval targets (Eino interrupt/resume).
func (e *Engine) ResumeStream(ctx context.Context, sessionID, runID string, targets map[string]any) (<-chan stream.StreamEvent, error) {
	if e == nil || e.runner == nil {
		return nil, ErrRuntimeClosed
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("session id required for resume")
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("resume targets required")
	}
	opts := []adk.AgentRunOption{adk.WithCheckPointID(sessionID)}
	opts = append(opts, usageRunOptions(types.Request{SessionID: sessionID, RequestID: runID}, e.agentID, e.tokenTracking)...)
	iter, err := e.runner.ResumeWithParams(ctx, sessionID, &adk.ResumeParams{Targets: targets}, opts...)
	if err != nil {
		return nil, err
	}
	return StreamEventsFromIterator(ctx, sessionID, runID, iter), nil
}

func (e *Engine) runOptions(req types.Request) []adk.AgentRunOption {
	opts := []adk.AgentRunOption{}
	if sid := strings.TrimSpace(req.SessionID); sid != "" {
		opts = append(opts, adk.WithCheckPointID(sid))
	}
	opts = append(opts, usageRunOptions(req, e.agentID, e.tokenTracking)...)
	return opts
}

func wrapRunContext(ctx context.Context, budget time.Duration) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok || budget <= 0 {
		return ctx
	}
	c, _ := context.WithTimeout(ctx, budget)
	return c
}
