package paths

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDir(t *testing.T) {
	env := func(key string) string {
		return os.Getenv(key)
	}
	dir := ResolveStateDir(env)
	if dir == "" {
		t.Fatal("expected non-empty state dir")
	}
	// Default should be ~/.openocta or contain .openocta
	if !filepath.IsAbs(dir) {
		t.Errorf("expected absolute path, got %q", dir)
	}
}

func TestResolveStateDirOverride(t *testing.T) {
	tmp := t.TempDir()
	os.Setenv("OPENOCTA_STATE_DIR", tmp)
	defer os.Unsetenv("OPENOCTA_STATE_DIR")
	env := func(key string) string { return os.Getenv(key) }
	dir := ResolveStateDir(env)
	if dir != tmp {
		t.Errorf("expected %q, got %q", tmp, dir)
	}
}

func TestDefaultGatewayPort(t *testing.T) {
	if DefaultGatewayPort() != 18900 {
		t.Errorf("expected 18900, got %d", DefaultGatewayPort())
	}
}

func TestResolveGatewayAddrWithBind(t *testing.T) {
	tests := []struct {
		name    string
		runMode string
		bind    string
		custom  string
		want    string
	}{
		{name: "desktop default remains loopback", runMode: "desktop", want: "127.0.0.1:18900"},
		{name: "service default remains all interfaces", runMode: "service", want: ":18900"},
		{name: "auto keeps run mode", runMode: "desktop", bind: "auto", want: "127.0.0.1:18900"},
		{name: "auto keeps service mode", runMode: "service", bind: "auto", want: ":18900"},
		{name: "lan overrides desktop loopback", runMode: "desktop", bind: "lan", want: ":18900"},
		{name: "lan alias 0.0.0.0 resolves all interfaces", runMode: "desktop", bind: "0.0.0.0", want: ":18900"},
		{name: "lan alias :: resolves all interfaces", runMode: "desktop", bind: "::", want: ":18900"},
		{name: "lan alias * resolves all interfaces", runMode: "desktop", bind: "*", want: ":18900"},
		{name: "loopback resolves loopback", runMode: "service", bind: "loopback", want: "127.0.0.1:18900"},
		{name: "loopback alias localhost resolves loopback", runMode: "service", bind: "localhost", want: "127.0.0.1:18900"},
		{name: "loopback alias 127.0.0.1 resolves loopback", runMode: "service", bind: "127.0.0.1", want: "127.0.0.1:18900"},
		{name: "loopback alias ::1 resolves loopback", runMode: "service", bind: "::1", want: "[::1]:18900"},
		{name: "tailnet listens on all interfaces", runMode: "desktop", bind: "tailnet", want: ":18900"},
		{name: "custom bind overrides run mode", runMode: "desktop", bind: "custom", custom: "192.168.1.10", want: "192.168.1.10:18900"},
		{name: "empty custom host preserves run mode", runMode: "desktop", bind: "custom", want: "127.0.0.1:18900"},
		{name: "explicit host is used as listen address", runMode: "desktop", bind: "192.168.1.20", want: "192.168.1.20:18900"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveGatewayAddrWithBind(18900, tt.runMode, tt.bind, tt.custom); got != tt.want {
				t.Fatalf("ResolveGatewayAddrWithBind() = %q, want %q", got, tt.want)
			}
		})
	}
}
