package terminal

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openocta/openocta/pkg/agent/tools"
	"golang.org/x/crypto/ssh"
)

// Local SSH transport + real bash. The fixture translates PTY line discipline
// for shell pipes, not a real OS PTY; command evaluation is not mocked.
func TestRealSSHAndBashExecutionRoundTrip(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil && runtime.GOOS == "windows" {
		git, e := exec.LookPath("git")
		if e == nil {
			bash = filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
			_, err = exec.LookPath(bash)
		}
	}
	if err != nil {
		t.Skip("bash is required for real-shell integration")
	}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(key)
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			c, e := listener.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				conn, chans, reqs, e := ssh.NewServerConn(c, config)
				if e != nil {
					return
				}
				defer conn.Close()
				go ssh.DiscardRequests(reqs)
				for request := range chans {
					if request.ChannelType() != "session" {
						request.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, requests, e := request.Accept()
					if e != nil {
						return
					}
					func() {
						defer ch.Close()
						for req := range requests {
							switch req.Type {
							case "pty-req", "window-change":
								req.Reply(true, nil)
							case "shell":
								cmd := exec.Command(bash, "--noprofile", "--norc")
								stdin, e := cmd.StdinPipe()
								if e != nil {
									return
								}
								cmd.Stdout = ch
								cmd.Stderr = ch
								if cmd.Start() != nil {
									return
								}
								req.Reply(true, nil)
								inputDone := make(chan struct{})
								go func() {
									defer close(inputDone)
									defer stdin.Close()
									reader := bufio.NewReader(ch)
									for {
										line, e := reader.ReadString('\r')
										if e != nil {
											return
										}
										line = strings.ReplaceAll(strings.TrimSuffix(line, "\r"), "\x15", "")
										io.WriteString(ch, line+"\r\n")
										io.WriteString(stdin, line+"\n")
									}
								}()
								cmd.Wait()
								ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								ch.Close()
								<-inputDone
								return
							default:
								req.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	manager := NewManager()
	defer func() { manager.Close(); listener.Close(); <-acceptDone; wg.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	id, _, err := manager.Connect(ctx, ConnectRequest{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Username: "fixture", Password: []byte("fixture"), HostKeyFingerprint: HostKeyFingerprint(signer.PublicKey())})
	if err != nil {
		t.Fatal(err)
	}
	// Manual input in the very same shell used by the tool.
	if err = manager.Input(id, []byte("cd /tmp\r")); err != nil {
		t.Fatal(err)
	}
	tool := tools.TerminalExecTool{Executor: manager, Allow: true, SessionID: id}
	for _, command := range []string{"pwd", "sleep 1; pwd", "false"} {
		started := time.Now()
		result, e := tool.Execute(ctx, map[string]interface{}{"command": command})
		if e != nil {
			t.Fatal(e)
		}
		var output ExecutionResult
		if e = json.Unmarshal([]byte(result.Output), &output); e != nil {
			t.Fatalf("invalid tool result %q: %v", result.Output, e)
		}
		if !output.Completed || output.ExitCode == nil || output.SessionID != id || output.RequestID == "" {
			t.Fatalf("bad result %s", result.Output)
		}
		if command == "false" {
			if result.Success || *output.ExitCode != 1 {
				t.Fatalf("false succeeded: %s", result.Output)
			}
		} else {
			if !result.Success || *output.ExitCode != 0 || !strings.Contains(output.Output, "/tmp") {
				t.Fatalf("cwd or completion wrong: %s", result.Output)
			}
		}
		if strings.HasPrefix(command, "sleep") && time.Since(started) < time.Second {
			t.Fatal("returned before actual command completed")
		}
		visible, e := manager.Output(id, 0)
		if e != nil || !strings.Contains(visible.Data, command) {
			t.Fatalf("terminal stream missing %q", command)
		}
		t.Logf("command=%q session=%s exit=%d completed=%t visible=true", command, id, *output.ExitCode, output.Completed)
	}
	wrong, _ := tool.Execute(ctx, map[string]interface{}{"sessionId": "other", "command": "pwd"})
	if wrong.Success {
		t.Fatal("wrong target accepted")
	}
}
