package terminal

import (
	"strings"
	"testing"
)

// buildWrapperEcho renders the stream a POSIX shell produces for an AI command
// executed through the eval wrapper.
func buildWrapperEcho(prompt, command, output, marker string) string {
	return prompt +
		"\x15eval " + quoteShellArg(command) + "; printf '\\036" + marker + "%d\\037\\n' \"$?\"\r" +
		output +
		"\x1e" + marker + "\x1f\n" +
		prompt
}

// TestScrubShowsAICommand verifies that the terminal shows the command the AI
// ran, followed by its output, instead of the internal wrapper syntax.
func TestScrubShowsAICommand(t *testing.T) {
	marker := "__OO_DONE_abc123def456abc123def456abc123:0"
	stream := buildWrapperEcho("[root@localhost ~]# ", "pwd", "/root\r\n", marker)
	visible := scrubTerminalOutput(stream)
	want := "[root@localhost ~]# pwd\n/root\r\n[root@localhost ~]# "
	if visible != want {
		t.Fatalf("display mismatch:\ngot:  %q\nwant: %q", visible, want)
	}
}

// TestScrubShowsAICommandWithQuote verifies that a command containing a single
// quote is displayed unescaped.
func TestScrubShowsAICommandWithQuote(t *testing.T) {
	marker := "__OO_DONE_abc123def456abc123def456abc123:0"
	stream := buildWrapperEcho("[root@localhost ~]# ", "echo 'it's'", "it's\r\n", marker)
	visible := scrubTerminalOutput(stream)
	want := "[root@localhost ~]# echo 'it's'\nit's\r\n[root@localhost ~]# "
	if visible != want {
		t.Fatalf("display mismatch:\ngot:  %q\nwant: %q", visible, want)
	}
}

// TestTrimPartialWrapper verifies that a wrapper echo split across two reads is
// held back until it can be matched as a whole.
func TestTrimPartialWrapper(t *testing.T) {
	full := "\x15eval 'pwd'; printf '\\036__OO_DONE_abc123:%d\\037\\n' \"$?\"\r"
	partial := full[:20]
	if trimmed := trimPartialWrapper(partial); trimmed != "" {
		t.Fatalf("partial wrapper should be held back, got %q", trimmed)
	}
	if trimmed := trimPartialWrapper(full); trimmed != full {
		t.Fatalf("complete wrapper should be kept, got %q", trimmed)
	}
}

// TestTrimPartialMarker verifies that a completion marker split across two
// reads is held back until it can be matched as a whole.
func TestTrimPartialMarker(t *testing.T) {
	full := "\x1e__OO_DONE_abc123def456abc123def456abc123:0\x1f"
	partial := full[:20]
	if trimmed := trimPartialWrapper(partial); trimmed != "" {
		t.Fatalf("partial marker should be held back, got %q", trimmed)
	}
	if trimmed := trimPartialWrapper(full); trimmed != full {
		t.Fatalf("complete marker should be kept, got %q", trimmed)
	}
}

// TestScrubKeepsOutputSharingMarkerLine verifies that output printed without a
// trailing newline survives the marker that follows it on the same line.
func TestScrubKeepsOutputSharingMarkerLine(t *testing.T) {
	stream := "no newline\x1e__OO_DONE_abc123def456abc123def456abc123:0\x1f\n"
	visible := scrubTerminalOutput(stream)
	if !strings.Contains(visible, "no newline") {
		t.Fatalf("output lost: %q", visible)
	}
	if strings.Contains(visible, "__OO_DONE_") {
		t.Fatalf("marker leaked: %q", visible)
	}
}
