package http

import (
	"net/http/httptest"
	"testing"

	"github.com/openocta/openocta/pkg/config"
	"github.com/openocta/openocta/pkg/gateway/handlers"
)

func TestValidateGatewayTokenAcceptsConfiguredPassword(t *testing.T) {
	mode := "password"
	server := &Server{
		ctx: &handlers.Context{
			LoadConfigSnapshot: func() (*handlers.ConfigSnapshot, error) {
				return &handlers.ConfigSnapshot{
					Config: &config.OpenOctaConfig{
						Gateway: &config.GatewayConfig{
							Auth: &config.GatewayAuthConfig{Mode: &mode, Password: "browser-password"},
						},
					},
				}, nil
			},
		},
	}

	valid := httptest.NewRequest("GET", "/api/health", nil)
	valid.Header.Set(headerAuthorization, "Bearer stale-token")
	valid.Header.Set(headerGatewayToken, "browser-password")
	if !server.validateGatewayToken(valid) {
		t.Fatal("expected configured password header to take precedence over a stale bearer token")
	}

	invalid := httptest.NewRequest("GET", "/api/health", nil)
	invalid.Header.Set(headerGatewayToken, "wrong-password")
	if server.validateGatewayToken(invalid) {
		t.Fatal("expected invalid password to be rejected")
	}
}

func TestValidateGatewayTokenRejectsPasswordModeWithoutPassword(t *testing.T) {
	mode := "password"
	server := &Server{
		ctx: &handlers.Context{
			LoadConfigSnapshot: func() (*handlers.ConfigSnapshot, error) {
				return &handlers.ConfigSnapshot{
					Config: &config.OpenOctaConfig{
						Gateway: &config.GatewayConfig{
							Auth: &config.GatewayAuthConfig{Mode: &mode},
						},
					},
				}, nil
			},
		},
	}
	if server.validateGatewayToken(httptest.NewRequest("GET", "/api/health", nil)) {
		t.Fatal("expected missing password to fail closed")
	}
}

func TestExtractRequestTokenAcceptsBearerCaseInsensitive(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set(headerAuthorization, "bearer   token-123 ")
	if got := extractRequestToken(req); got != "token-123" {
		t.Fatalf("extractRequestToken() = %q, want %q", got, "token-123")
	}

	req = httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set(headerAuthorization, "BEARER\tcase-mixed")
	if got := extractRequestToken(req); got != "case-mixed" {
		t.Fatalf("extractRequestToken() = %q, want %q", got, "case-mixed")
	}
}

func TestValidateGatewayTokenAllowsRequestsWhenAuthIsNotConfigured(t *testing.T) {
	t.Setenv("OPENOCTA_GATEWAY_TOKEN", "")
	server := &Server{
		ctx: &handlers.Context{
			LoadConfigSnapshot: func() (*handlers.ConfigSnapshot, error) {
				return &handlers.ConfigSnapshot{Config: &config.OpenOctaConfig{}}, nil
			},
		},
	}
	if !server.validateGatewayToken(httptest.NewRequest("GET", "/api/health", nil)) {
		t.Fatal("expected requests to remain allowed when auth is not configured")
	}
}
