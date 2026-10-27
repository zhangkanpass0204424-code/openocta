package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestScrubTerminalOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "removes Ctrl-U wrapper echo",
			input:    string([]byte{0x15}) + "eval 'pwd'; printf '\\036__OO_DONE_test:0\\037' \"$0\"\n[root@localhost ~]# \n/root\n",
			expected: "\n[root@localhost ~]# \n/root\n",
		},
		{
			name:     "removes completion markers",
			input:    "normal prompt\n__OO_DONE_abcd1234:5\n[root@localhost ~]# ",
			expected: "normal prompt\n[root@localhost ~]# ",
		},
		{
			name:     "preserves normal output",
			input:    "Last login: Tue Oct 27 08:50:22 2026 from 10.10.10.88\n[root@localhost ~]# pwd\n/root\n[root@localhost ~]# ",
			expected: "Last login: Tue Oct 27 08:50:22 2026 from 10.10.10.88\n[root@localhost ~]# pwd\n/root\n[root@localhost ~]# ",
		},
		{
			name:     "handles mixed content",
			input:    "echo hello\n" + string([]byte{0x1e}) + "__OO_DONE_test:0\nhello\n goodbye",
			expected: "echo hello\nhello\n goodbye",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := scrubTerminalOutput(tc.input)
			if result != tc.expected {
				t.Errorf("scrub mismatch:\ngot:  %q\nwant: %q", result, tc.expected)
			}
		})
	}
}

type executionWriter struct{ fn func(string) }

func (w executionWriter) Write(p []byte) (int, error) { w.fn(string(p)); return len(p), nil }
func (w executionWriter) Close() error                { return nil }

func TestExecutionWaitsForMarkerAndRetainsShellState(t *testing.T) {
	m := NewManager()
	s := &session{}
	m.sessions["target"] = s
	cwd := "/root"
	markerPattern := regexp.MustCompile(`__OO_DONE_[a-z0-9]+:`)
	s.stdin = executionWriter{fn: func(wire string) {
		marker := markerPattern.FindString(wire)
		if strings.Contains(wire, "cd /tmp") {
			cwd = "/tmp"
		}
		output := cwd
		// Echo alone and pauses must never count as completion.
		s.Write([]byte(wire + "\n"))
		go func() {
			time.Sleep(450 * time.Millisecond)
			s.Write([]byte(output + "\r\n\x1e" + marker + "0"))
			time.Sleep(40 * time.Millisecond)
			s.Write([]byte("\x1f\r\n"))
		}()
	}}
	for _, cmd := range []string{"cd /tmp", "sleep 0.4; pwd"} {
		start := time.Now()
		raw, err := m.ExecuteCommand(context.Background(), "target", cmd)
		if err != nil {
			t.Fatal(err)
		}
		var result ExecutionResult
		if err = json.Unmarshal([]byte(raw), &result); err != nil {
			t.Fatal(err)
		}
		if !result.Completed || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Output, "/tmp") {
			t.Fatalf("bad result: %s", raw)
		}
		if time.Since(start) < 450*time.Millisecond {
			t.Fatal("returned on echo or idle time")
		}
		visible, _ := m.Output("target", 0)
		if !strings.Contains(visible.Data, cmd) {
			t.Fatal("command not visible")
		}
	}
}

func TestExecutionTimeoutBlocksFurtherInput(t *testing.T) {
	m := NewManager()
	s := &session{stdin: executionWriter{fn: func(string) {}}}
	m.sessions["target"] = s
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	raw, err := m.ExecuteCommand(ctx, "target", "sleep 10")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%s %v", raw, err)
	}
	if err = m.Input("target", []byte("pwd\r")); !errors.Is(err, ErrExecutionUncertain) {
		t.Fatalf("unexpected input: %v", err)
	}
}

func TestExecutionRejectsConcurrentInputAndReportsFailureExit(t *testing.T) {
	m := NewManager()
	s := &session{}
	m.sessions["target"] = s
	s.stdin = executionWriter{fn: func(wire string) {
		if err := m.Input("target", []byte("unsafe")); !errors.Is(err, ErrSessionBusy) {
			t.Errorf("input not blocked: %v", err)
		}
		marker := regexp.MustCompile(`__OO_DONE_[a-z0-9]+:`).FindString(wire)
		s.Write([]byte("failure\n\x1e" + marker + "7\x1f\n"))
	}}
	raw, err := m.ExecuteCommand(context.Background(), "target", "false")
	if err != nil {
		t.Fatal(err)
	}
	var result ExecutionResult
	json.Unmarshal([]byte(raw), &result)
	if result.ExitCode == nil || *result.ExitCode != 7 {
		t.Fatalf("%s", raw)
	}
}

var _ io.WriteCloser = executionWriter{}
