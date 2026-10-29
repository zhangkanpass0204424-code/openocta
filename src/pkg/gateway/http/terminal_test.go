package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openocta/openocta/pkg/config"
	"github.com/openocta/openocta/pkg/gateway/handlers"
	gatewayterminal "github.com/openocta/openocta/pkg/gateway/terminal"
)

func TestValidateTerminalSessionRequest(t *testing.T) {
	req := terminalSessionRequest{
		Host:     "server.example.com",
		Username: "operator",
		Password: secretBytes("secret"),
	}
	validated, err := validateTerminalSessionRequest(req)
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if validated.Port != 22 || validated.Host != req.Host || validated.Username != req.Username {
		t.Fatalf("unexpected normalized request: %+v", validated)
	}

	tests := []struct {
		name string
		edit func(*terminalSessionRequest)
	}{
		{"missing authentication after host key confirmation", func(r *terminalSessionRequest) {
			r.Password = nil
			r.HostKeyFingerprint = "SHA256:confirmed"
		}},
		{"host path rejected", func(r *terminalSessionRequest) { r.Host = "server.example.com/path" }},
		{"invalid port rejected", func(r *terminalSessionRequest) { r.Port = 65536 }},
		{"control character username rejected", func(r *terminalSessionRequest) { r.Username = "operator\n" }},
		{"passphrase requires key", func(r *terminalSessionRequest) { r.Passphrase = secretBytes("phrase") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := req
			test.edit(&candidate)
			if _, err := validateTerminalSessionRequest(candidate); err == nil {
				t.Fatal("expected invalid request to be rejected")
			}
		})
	}
}

func TestValidateTerminalSessionRequestAllowsUnauthenticatedHostKeyProbe(t *testing.T) {
	req := terminalSessionRequest{
		Host:     "server.example.com",
		Username: "operator",
	}
	validated, err := validateTerminalSessionRequest(req)
	if err != nil {
		t.Fatalf("host-key probe rejected: %v", err)
	}
	if len(validated.Password) != 0 || len(validated.PrivateKey) != 0 {
		t.Fatal("host-key probe unexpectedly includes credentials")
	}
	req.HostKeyFingerprint = "SHA256:confirmed"
	if _, err := validateTerminalSessionRequest(req); err == nil {
		t.Fatal("expected authenticated connection to require credentials")
	}
}

func TestSecretBytesJSONAndClear(t *testing.T) {
	var secret secretBytes
	if err := json.Unmarshal([]byte(`"not-retained"`), &secret); err != nil {
		t.Fatal(err)
	}
	if string(secret) != "not-retained" {
		t.Fatalf("unexpected decoded credential %q", secret)
	}
	secret.clear()
	if len(secret) != 0 {
		t.Fatal("expected credential bytes to be cleared")
	}
}

func TestTerminalEndpointsRequireGatewayAuth(t *testing.T) {
	mode := "password"
	server := &Server{
		mux:             http.NewServeMux(),
		terminalManager: gatewayterminal.NewManager(),
		ctx: &handlers.Context{
			LoadConfigSnapshot: func() (*handlers.ConfigSnapshot, error) {
				return &handlers.ConfigSnapshot{
					Config: &config.OpenOctaConfig{
						Gateway: &config.GatewayConfig{
							Auth: &config.GatewayAuthConfig{Mode: &mode, Password: "gateway-secret"},
						},
					},
				}, nil
			},
		},
	}

	defer server.terminalManager.Close()
	server.registerRoutes()
	handler := server.Handler()

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/terminal/hosts", ""},
		{http.MethodDelete, "/api/terminal/hosts/host-1", ""},
		{http.MethodPost, "/api/terminal/sessions", `{}`},
		{http.MethodGet, "/api/terminal/sessions/session-1/output", ""},
		{http.MethodPost, "/api/terminal/sessions/session-1/input", `{"data":"x"}`},
		{http.MethodPost, "/api/terminal/sessions/session-1/resize", `{"cols":80,"rows":24}`},
		{http.MethodDelete, "/api/terminal/sessions/session-1", ""},
	}
	for _, request := range requests {
		t.Run(request.method+" "+request.path, func(t *testing.T) {
			req := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected unauthorized response, got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTerminalPreflightAllowsAuthenticatedCrossOriginRequests(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	server.registerRoutes()
	request := httptest.NewRequest(http.MethodOptions, "/api/terminal/sessions", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected preflight status 204, got %d", response.Code)
	}
	if response.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("expected terminal preflight to allow the Control UI origin")
	}
	if !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "X-Gateway-Token") {
		t.Fatal("expected terminal preflight to allow gateway auth header")
	}
}

func TestTerminalHostsListAndDelete(t *testing.T) {
	store, err := gatewayterminal.NewHostStore(filepath.Join(t.TempDir(), "hosts.json"))
	if err != nil {
		t.Fatal(err)
	}
	host, err := store.Upsert(gatewayterminal.SavedHost{
		Host: "10.10.10.16", Port: 22, Username: "root", Fingerprint: "SHA256:test",
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{terminalHostStore: store}

	listResponse := httptest.NewRecorder()
	server.handleTerminalHostsList(listResponse, httptest.NewRequest(http.MethodGet, "/api/terminal/hosts", nil))
	if listResponse.Code != http.StatusOK {
		t.Fatalf("expected host list status 200, got %d", listResponse.Code)
	}
	var hosts []gatewayterminal.SavedHost
	if err := json.Unmarshal(listResponse.Body.Bytes(), &hosts); err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 1 || hosts[0].ID != host.ID || hosts[0].Host != "10.10.10.16" {
		t.Fatalf("unexpected host list: %+v", hosts)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/terminal/hosts/"+host.ID, nil)
	deleteRequest.SetPathValue("id", host.ID)
	deleteResponse := httptest.NewRecorder()
	server.handleTerminalHostDelete(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusNoContent {
		t.Fatalf("expected delete status 204, got %d: %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if hosts := store.List(); len(hosts) != 0 {
		t.Fatalf("expected host deletion, got %+v", hosts)
	}
}
