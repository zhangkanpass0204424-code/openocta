package handlers

import (
	"crypto/subtle"
	"os"
	"strings"
)

const envGatewayToken = "OPENOCTA_GATEWAY_TOKEN"

// GetExpectedGatewayToken returns the active gateway credential from config or env.
// When password mode is enabled, the configured password takes precedence over the token.
func GetExpectedGatewayToken(loadSnapshot func() (*ConfigSnapshot, error)) string {
	secret, _ := GetExpectedGatewayAuth(loadSnapshot)
	return secret
}

// GetExpectedGatewayAuth returns the configured credential and whether auth is required.
// Password mode requires a password even when it is missing, so an incomplete config fails closed.
func GetExpectedGatewayAuth(loadSnapshot func() (*ConfigSnapshot, error)) (string, bool) {
	if loadSnapshot != nil {
		snap, err := loadSnapshot()
		if err == nil && snap != nil && snap.Config != nil &&
			snap.Config.Gateway != nil && snap.Config.Gateway.Auth != nil {
			auth := snap.Config.Gateway.Auth
			if auth.Mode != nil && strings.EqualFold(strings.TrimSpace(*auth.Mode), "password") {
				return strings.TrimSpace(auth.Password), true
			}
			token := strings.TrimSpace(auth.Token)
			if token != "" {
				return token, true
			}
		}
	}
	token := strings.TrimSpace(os.Getenv(envGatewayToken))
	return token, token != ""
}

// GatewayCredentialMatches compares credentials without leaking the matching prefix via timing.
func GatewayCredentialMatches(got, expected string) bool {
	return expected != "" && subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
