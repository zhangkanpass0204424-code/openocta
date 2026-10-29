package terminal

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestHostKeyFingerprintRequiresExactMatch(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := ssh.NewPublicKey(privateKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := HostKeyFingerprint(publicKey)
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Fatalf("unexpected fingerprint format %q", fingerprint)
	}
	if !hostKeyMatches(fingerprint, publicKey) {
		t.Fatal("expected exact fingerprint match")
	}
	if hostKeyMatches(fingerprint+"x", publicKey) || hostKeyMatches("", publicKey) {
		t.Fatal("expected non-exact and empty fingerprints to fail")
	}
}

func TestManagerCapacityAndBoundedOutput(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	for i := 0; i < maxActiveSessions; i++ {
		if err := manager.reserve(); err != nil {
			t.Fatalf("reserve %d: %v", i, err)
		}
	}
	if err := manager.reserve(); !errors.Is(err, ErrCapacity) {
		t.Fatalf("expected capacity error, got %v", err)
	}
	for i := 0; i < maxActiveSessions; i++ {
		manager.release()
	}

	state := &session{}
	manager.sessions["test"] = state
	if _, err := state.Write([]byte(strings.Repeat("a", maxOutputBytes/2))); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", maxOutputBytes+10)
	if _, err := state.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	output, err := manager.Output("test", 0)
	if err != nil {
		t.Fatal(err)
	}
	expectedBase := int64(maxOutputBytes/2 + 10)
	if output.Offset != expectedBase+maxOutputRead || len(output.Data) != maxOutputRead {
		t.Fatalf("unexpected first output window: offset=%d length=%d", output.Offset, len(output.Data))
	}
	end := int64(maxOutputBytes + maxOutputBytes/2 + 10)
	output, err = manager.Output("test", end)
	if err != nil {
		t.Fatal(err)
	}
	if output.Offset != end || len(output.Data) != 0 {
		t.Fatalf("unexpected second output window: offset=%d length=%d", output.Offset, len(output.Data))
	}
}

func TestConnectConfirmsHostKeyBeforeAuthentication(t *testing.T) {
	for _, test := range []struct {
		name        string
		fingerprint string
		wantError   error
	}{
		{name: "first use", wantError: ErrHostKeyConfirmationRequired},
		{name: "mismatch", fingerprint: "SHA256:not-the-server-key", wantError: ErrHostKeyMismatch},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, authAttempts, stop := startTestSSHServer(t)
			defer stop()
			port := listener.Addr().(*net.TCPAddr).Port
			manager := NewManager()
			defer manager.Close()

			_, fingerprint, err := manager.Connect(context.Background(), ConnectRequest{
				Host:               "127.0.0.1",
				Port:               port,
				Username:           "test-user",
				Password:           []byte("must-not-be-sent"),
				HostKeyFingerprint: test.fingerprint,
			})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("expected %v, got %v", test.wantError, err)
			}
			if test.fingerprint == "" && !strings.HasPrefix(fingerprint, "SHA256:") {
				t.Fatalf("expected server fingerprint, got %q", fingerprint)
			}
			if attempts := authAttempts.Load(); attempts != 0 {
				t.Fatalf("expected no authentication attempts, got %d", attempts)
			}
		})
	}
}

func startTestSSHServer(t *testing.T) (net.Listener, *atomic.Int32, func()) {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	authAttempts := &atomic.Int32{}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) {
			authAttempts.Add(1)
			return nil, errors.New("not authorized")
		},
	}
	serverConfig.AddHostKey(signer)
	var connections sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			go func() {
				defer connections.Done()
				_, _, _, _ = ssh.NewServerConn(conn, serverConfig)
				_ = conn.Close()
			}()
		}
	}()
	stop := func() {
		_ = listener.Close()
		<-done
		connections.Wait()
	}
	return listener, authAttempts, stop
}
