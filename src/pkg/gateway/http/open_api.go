package http

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/openocta/openocta/pkg/apikeys"
	"github.com/openocta/openocta/pkg/gateway/handlers"
	"github.com/openocta/openocta/pkg/logging"
)

var openAPILog = logging.Sub("open-api")

func extractOpenAPIToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if got := strings.TrimSpace(r.Header.Get("X-OpenOcta-Api-Key")); got != "" {
		return got
	}
	if got := strings.TrimSpace(r.Header.Get("X-OpenOcta-Token")); got != "" {
		return got
	}
	return ""
}

func writeOpenAPIJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeOpenAPIError(w http.ResponseWriter, status int, message string) {
	writeOpenAPIJSON(w, status, map[string]string{"error": message})
}

func resolveOpenAPIPath(r *http.Request) string {
	path := normalizeOpenAPIPath(r.URL.Path)
	if path == "/ping" {
		return "/openocta/open/v1/ping"
	}
	return path
}

func normalizeOpenAPIPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

type openCompletionRequest struct {
	Model          string                   `json:"model"`
	Messages       []map[string]interface{} `json:"messages"`
	Stream         bool                     `json:"stream"`
	ConversationID string                   `json:"conversation_id"`
}

// handleOpenAPI serves third-party open endpoints authenticated by API keys.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if s.ctx == nil || s.ctx.ApiKeyService == nil {
		writeOpenAPIError(w, http.StatusServiceUnavailable, "open api not configured")
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeOpenAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token := extractOpenAPIToken(r)
	if token == "" {
		writeOpenAPIError(w, http.StatusUnauthorized, "missing api key")
		return
	}
	rec, err := s.ctx.ApiKeyService.Authenticate(token)
	if err != nil {
		writeOpenAPIError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	fullPath := resolveOpenAPIPath(r)
	if !apikeys.PathAllowed(rec, fullPath) {
		writeOpenAPIError(w, http.StatusForbidden, "path not allowed")
		return
	}

	subpath := strings.TrimPrefix(fullPath, "/openocta/open/v1")
	subpath = strings.TrimPrefix(subpath, "/")
	subpath = strings.ToLower(subpath)
	if subpath == "" && fullPath == "/ping" {
		subpath = "ping"
	}

	switch subpath {
	case "ping":
		s.handleOpenAPIPing(w, r, rec)
		return
	case "completion":
		if r.Method != http.MethodPost {
			writeOpenAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleOpenAPICompletion(w, r, rec)
		return
	default:
		writeOpenAPIError(w, http.StatusNotFound, "not found")
	}
}

func (s *Server) handleOpenAPIPing(w http.ResponseWriter, r *http.Request, rec *apikeys.Record) {
	writeOpenAPIJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"ts":        time.Now().UnixMilli(),
		"keyId":     rec.ID,
		"name":      rec.Name,
		"keyPrefix": rec.KeyPrefix,
		"message":   "pong",
	})
}

func (s *Server) handleOpenAPICompletion(w http.ResponseWriter, r *http.Request, rec *apikeys.Record) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeOpenAPIError(w, http.StatusBadRequest, "invalid body")
		return
	}
	var req openCompletionRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			writeOpenAPIError(w, http.StatusBadRequest, "invalid json")
			return
		}
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		writeOpenAPIError(w, http.StatusBadRequest, "model required")
		return
	}
	if !apikeys.ModelAllowed(rec, model) {
		writeOpenAPIError(w, http.StatusForbidden, "model not allowed")
		return
	}
	if err := apikeys.CheckTokenBudget(rec, 1); err != nil {
		writeOpenAPIError(w, http.StatusTooManyRequests, err.Error())
		return
	}

	out, status, msg := handlers.ExecuteOpenAPICompletion(s.ctx, r.Context(), rec, handlers.OpenAPICompletionInput{
		Model:          model,
		Messages:       req.Messages,
		Stream:         req.Stream,
		ConversationID: req.ConversationID,
	})
	if status != http.StatusOK {
		writeOpenAPIError(w, status, msg)
		return
	}

	openAPILog.Info("open completion finished keyId=%s model=%s tokens=%d", rec.ID, model, out.TotalTokens)
	writeOpenAPIJSON(w, http.StatusOK, map[string]interface{}{
		"id":      "open-" + rec.ID,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   out.Model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"message": map[string]string{
					"role":    "assistant",
					"content": out.Content,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]int64{
			"prompt_tokens":     out.PromptTokens,
			"completion_tokens": out.CompletionTokens,
			"total_tokens":      out.TotalTokens,
		},
	})
}
