package handlers

import (
	"testing"

	"github.com/openocta/openocta/pkg/config"
)

func TestGetExpectedGatewayAuth(t *testing.T) {
	t.Setenv(envGatewayToken, "environment-token")

	tests := []struct {
		name     string
		auth     *config.GatewayAuthConfig
		expected string
		required bool
	}{
		{
			name:     "token mode",
			auth:     &config.GatewayAuthConfig{Token: " configured-token "},
			expected: "configured-token",
			required: true,
		},
		{
			name: "password mode",
			auth: &config.GatewayAuthConfig{
				Mode:     stringPtr("password"),
				Token:    "ignored-token",
				Password: " configured-password ",
			},
			expected: "configured-password",
			required: true,
		},
		{
			name: "password mode without password fails closed",
			auth: &config.GatewayAuthConfig{
				Mode:  stringPtr("password"),
				Token: "ignored-token",
			},
			expected: "",
			required: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadSnapshot := func() (*ConfigSnapshot, error) {
				return &ConfigSnapshot{
					Config: &config.OpenOctaConfig{
						Gateway: &config.GatewayConfig{Auth: tt.auth},
					},
				}, nil
			}
			got, required := GetExpectedGatewayAuth(loadSnapshot)
			if got != tt.expected || required != tt.required {
				t.Fatalf("GetExpectedGatewayAuth() = (%q, %t), want (%q, %t)", got, required, tt.expected, tt.required)
			}
		})
	}

	got, required := GetExpectedGatewayAuth(nil)
	if got != "environment-token" || !required {
		t.Fatalf("environment fallback = (%q, %t), want (%q, true)", got, required, "environment-token")
	}
}

func TestGatewayCredentialMatches(t *testing.T) {
	if !GatewayCredentialMatches("secret", "secret") {
		t.Fatal("expected matching credentials to be accepted")
	}
	if GatewayCredentialMatches("secret", "other") {
		t.Fatal("expected mismatched credentials to be rejected")
	}
	if GatewayCredentialMatches("secret", "") {
		t.Fatal("expected an empty configured credential to be rejected")
	}
}

func stringPtr(value string) *string {
	return &value
}
