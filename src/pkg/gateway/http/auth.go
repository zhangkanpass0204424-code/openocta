// Package http provides gateway token validation for HTTP API handlers.
package http

import (
	"net/http"
	"strings"

	"github.com/openocta/openocta/pkg/gateway/handlers"
)

const (
	headerAuthorization = "Authorization"
	headerGatewayToken  = "X-Gateway-Token"
)

func (s *Server) expectedGatewayAuth() (string, bool) {
	var loadSnapshot func() (*handlers.ConfigSnapshot, error)
	if s.ctx != nil {
		loadSnapshot = s.ctx.LoadConfigSnapshot
	}
	return handlers.GetExpectedGatewayAuth(loadSnapshot)
}

// extractRequestToken extracts token from Authorization: Bearer <token> or X-Gateway-Token.
// Also accepts ?token= query param — the preview iframe navigation cannot attach
// headers, so the front-end appends the token to preview URLs (localFilePreviewUrl).
func extractRequestToken(r *http.Request) string {
	if t := strings.TrimSpace(r.Header.Get(headerGatewayToken)); t != "" {
		return t
	}
	if ah := strings.TrimSpace(r.Header.Get(headerAuthorization)); ah != "" {
		parts := strings.Fields(ah)
		if len(parts) >= 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(strings.TrimPrefix(ah, parts[0]))
		}
	}
	if t := strings.TrimSpace(r.URL.Query().Get("token")); t != "" {
		return t
	}
	return ""
}

// validateGatewayToken returns true if the request has a valid credential.
// Requests are allowed when authentication is not configured.
func (s *Server) validateGatewayToken(r *http.Request) bool {
	expected, required := s.expectedGatewayAuth()
	if !required {
		return true
	}
	got := extractRequestToken(r)
	return handlers.GatewayCredentialMatches(got, expected)
}

// writeTokenError writes 401 JSON response with a clear message.
func writeTokenError(w http.ResponseWriter) {
	setSiteProxyCORSHeaders(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"网关密码或令牌无效或未提供。请检查 gateway.auth.password 或 Gateway Token 配置。","code":"invalid_gateway_token"}`))
}

// requireGatewayToken wraps an http.HandlerFunc and validates the gateway token before calling it.
func (s *Server) requireGatewayToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.validateGatewayToken(r) {
			writeTokenError(w)
			return
		}
		next(w, r)
	}
}
