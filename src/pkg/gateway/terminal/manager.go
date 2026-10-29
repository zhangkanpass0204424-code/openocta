package terminal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

const (
	maxActiveSessions = 8
	maxRetained       = 32
	maxOutputBytes    = 1 << 20
	maxOutputRead     = 64 << 10
	connectTimeout    = 10 * time.Second
)

var (
	ErrHostKeyConfirmationRequired = errors.New("host key confirmation required")
	ErrHostKeyMismatch             = errors.New("host key fingerprint mismatch")
	ErrCapacity                    = errors.New("terminal session capacity reached")
	ErrSessionNotFound             = errors.New("terminal session not found")
	ErrSessionClosed               = errors.New("terminal session is closed")
)

type ConnectRequest struct {
	Host               string
	Port               int
	Username           string
	Password           []byte
	PrivateKey         []byte
	Passphrase         []byte
	HostKeyFingerprint string
}

type Output struct {
	Data   string
	Offset int64
	Closed bool
	Error  string
}

type Manager struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	slots    int
	closed   bool
	sessions map[string]*session
}

type session struct {
	mu        sync.Mutex
	execMu    sync.Mutex
	uncertain bool
	client    *ssh.Client
	ssh       *ssh.Session
	stdin     io.WriteCloser
	output    []byte
	base      int64
	closed    bool
	err       string
}

func NewManager() *Manager {
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{
		ctx:      ctx,
		cancel:   cancel,
		sessions: make(map[string]*session),
	}
}

// HostKeyFingerprint formats the server key in OpenSSH SHA256 form.
func HostKeyFingerprint(key ssh.PublicKey) string {
	return ssh.FingerprintSHA256(key)
}

func hostKeyMatches(expected string, key ssh.PublicKey) bool {
	return expected != "" && expected == HostKeyFingerprint(key)
}

func (m *Manager) Connect(ctx context.Context, req ConnectRequest) (string, string, error) {
	if err := m.reserve(); err != nil {
		return "", "", err
	}
	reserved := true
	defer func() {
		if reserved {
			m.release()
		}
	}()

	address := net.JoinHostPort(req.Host, fmt.Sprint(req.Port))
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	stopCancel := context.AfterFunc(m.ctx, cancel)
	defer func() {
		stopCancel()
		cancel()
	}()

	conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	if err != nil {
		return "", "", err
	}
	stopConnClose := context.AfterFunc(dialCtx, func() { _ = conn.Close() })
	defer stopConnClose()
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))

	var fingerprint string
	config := &ssh.ClientConfig{
		User: req.Username,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint = HostKeyFingerprint(key)
			if req.HostKeyFingerprint == "" {
				return ErrHostKeyConfirmationRequired
			}
			if !hostKeyMatches(req.HostKeyFingerprint, key) {
				return ErrHostKeyMismatch
			}
			return nil
		},
	}

	if req.HostKeyFingerprint == "" {
		_, _, _, handshakeErr := ssh.NewClientConn(conn, address, config)
		_ = conn.Close()
		if fingerprint != "" {
			return "", fingerprint, ErrHostKeyConfirmationRequired
		}
		if handshakeErr != nil {
			return "", "", handshakeErr
		}
		return "", "", ErrHostKeyConfirmationRequired
	}

	config.Auth = authMethods(req.Password, req.PrivateKey, req.Passphrase)
	if len(config.Auth) == 0 {
		_ = conn.Close()
		return "", fingerprint, errors.New("SSH authentication credentials are required")
	}
	rawConn, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		if errors.Is(err, ErrHostKeyMismatch) {
			return "", fingerprint, ErrHostKeyMismatch
		}
		return "", fingerprint, err
	}
	_ = conn.SetDeadline(time.Now().Add(connectTimeout))
	client := ssh.NewClient(rawConn, channels, requests)
	term, err := client.NewSession()
	if err != nil {
		_ = client.Close()
		return "", fingerprint, err
	}
	stdin, err := term.StdinPipe()
	if err != nil {
		_ = term.Close()
		_ = client.Close()
		return "", fingerprint, err
	}
	state := &session{client: client, ssh: term, stdin: stdin}
	term.Stdout = state
	term.Stderr = state
	if err := term.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err != nil {
		_ = term.Close()
		_ = client.Close()
		return "", fingerprint, err
	}
	if err := term.Shell(); err != nil {
		_ = term.Close()
		_ = client.Close()
		return "", fingerprint, err
	}
	if err := dialCtx.Err(); err != nil {
		_ = term.Close()
		_ = client.Close()
		return "", fingerprint, err
	}
	_ = conn.SetDeadline(time.Time{})
	stopConnClose()

	id := uuid.NewString()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = term.Close()
		_ = client.Close()
		return "", fingerprint, ErrSessionClosed
	}
	m.pruneClosedLocked()
	m.sessions[id] = state
	m.mu.Unlock()

	reserved = false // the slot is released by waitForSession
	go m.waitForSession(state)
	return id, fingerprint, nil
}

func authMethods(password, privateKey, passphrase []byte) []ssh.AuthMethod {
	methods := make([]ssh.AuthMethod, 0, 2)
	if len(password) != 0 {
		methods = append(methods, ssh.Password(string(password)))
	}
	if len(privateKey) != 0 {
		var signer ssh.Signer
		var err error
		if len(passphrase) != 0 {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(privateKey, passphrase)
		} else {
			signer, err = ssh.ParsePrivateKey(privateKey)
		}
		if err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	}
	return methods
}

func (m *Manager) reserve() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrSessionClosed
	}
	if m.slots >= maxActiveSessions {
		return ErrCapacity
	}
	m.slots++
	return nil
}

func (m *Manager) release() {
	m.mu.Lock()
	if m.slots > 0 {
		m.slots--
	}
	m.mu.Unlock()
}

func (m *Manager) waitForSession(state *session) {
	err := state.ssh.Wait()
	state.mu.Lock()
	state.closed = true
	if err != nil {
		state.err = err.Error()
	}
	state.mu.Unlock()
	m.release()
}

func (m *Manager) pruneClosedLocked() {
	if len(m.sessions) < maxRetained {
		return
	}
	for id, candidate := range m.sessions {
		candidate.mu.Lock()
		closed := candidate.closed
		candidate.mu.Unlock()
		if closed {
			delete(m.sessions, id)
			if len(m.sessions) < maxRetained {
				return
			}
		}
	}
}

func (s *session) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return len(data), nil
	}
	if len(data) >= maxOutputBytes {
		s.base += int64(len(s.output) + len(data) - maxOutputBytes)
		s.output = append(s.output[:0], data[len(data)-maxOutputBytes:]...)
		return len(data), nil
	}
	s.output = append(s.output, data...)
	if excess := len(s.output) - maxOutputBytes; excess > 0 {
		s.output = append(s.output[:0], s.output[excess:]...)
		s.base += int64(excess)
	}
	return len(data), nil
}

func (m *Manager) Output(id string, offset int64) (Output, error) {
	if offset < 0 {
		return Output{}, errors.New("offset must not be negative")
	}
	out, err := m.rawOutput(id, offset)
	if err != nil {
		return out, err
	}
	// Hold back a trailing partial wrapper echo or marker so the next poll can
	// match it as a whole instead of leaking internal syntax to the user.
	if !out.Closed {
		if trimmed := trimPartialWrapper(out.Data); len(trimmed) != len(out.Data) {
			out.Data = trimmed
			out.Offset = offset + int64(len(trimmed))
		}
	}
	out.Data = scrubTerminalOutput(out.Data)
	return out, nil
}

// trimPartialWrapper cuts a trailing partial wrapper echo or completion marker
// so that it is re-read together with the bytes that complete it.
func trimPartialWrapper(data string) string {
	cut := -1
	if idx := strings.LastIndex(data, "\x15"); idx >= 0 && strings.HasPrefix(data[idx+1:], "eval ") {
		if !evalWrapperEcho.MatchString(data[idx+1:]) {
			cut = idx
		}
	}
	if idx := strings.LastIndex(data, "\x1e"); idx >= 0 && strings.HasPrefix(data[idx+1:], "__OO_DONE_") {
		if !markerFrame.MatchString(data[idx:]) && idx > cut {
			cut = idx
		}
	}
	if cut < 0 {
		return data
	}
	return data[:cut]
}

// rawOutput returns the terminal stream without scrubbing. The execution loop
// relies on it to observe the completion marker, which scrubTerminalOutput
// intentionally removes from the stream shown to the user.
func (m *Manager) rawOutput(id string, offset int64) (Output, error) {
	state, err := m.get(id)
	if err != nil {
		return Output{}, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	end := state.base + int64(len(state.output))
	if offset < state.base {
		offset = state.base
	}
	if offset > end {
		offset = end
	}
	start := offset - state.base
	readEnd := start + maxOutputRead
	if readEnd > int64(len(state.output)) {
		readEnd = int64(len(state.output))
	}
	return Output{
		Data:   string(state.output[start:readEnd]),
		Offset: offset + (readEnd - start),
		Closed: state.closed,
		Error:  state.err,
	}, nil
}

// scrubTerminalOutput removes wrapper commands and completion markers from terminal output.
// Filters: Ctrl-U lines, eval/printf wrapper echoes, and __OO_DONE_ markers.
func (m *Manager) Input(id string, data []byte) error {
	state, err := m.get(id)
	if err != nil {
		return err
	}
	// The terminal is locked only while an AI command is actually running.
	if !state.execMu.TryLock() {
		return ErrSessionBusy
	}
	defer state.execMu.Unlock()
	state.mu.Lock()
	closed, stdin, uncertain := state.closed, state.stdin, state.uncertain
	state.mu.Unlock()
	if closed {
		return ErrSessionClosed
	}
	if uncertain {
		// The previous AI command timed out and may still be running in the
		// shell. Interrupt it so the human regains control of the terminal
		// instead of being locked out until the session is reconnected.
		state.mu.Lock()
		state.uncertain = false
		state.mu.Unlock()
		if _, err := stdin.Write([]byte("\x03")); err != nil {
			return err
		}
	}
	_, err = stdin.Write(data)
	return err
}

var ErrSessionBusy = errors.New("terminal is executing an AI command")
var ErrExecutionUncertain = errors.New("previous execution did not finish; reconnect before sending more commands")
var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var markerOnly = regexp.MustCompile(`__OO_DONE_[a-z0-9]+:`)

// evalWrapperEcho matches the wrapper command as the shell echoes it back,
// e.g.  eval 'ls -la'; printf '\036__OO_DONE_<id>:%d\037\n' "$?"
var evalWrapperEcho = regexp.MustCompile(`eval '(.*?)'; printf '\\036[^']*' "\$[^"]*"`)

// markerFrame matches the framed completion marker emitted by the wrapper.
var markerFrame = regexp.MustCompile("\x1e__OO_DONE_[a-z0-9]+:-?\\d+\x1f")

// quoteShellArg wraps a command in single quotes for the POSIX shell.
func quoteShellArg(command string) string {
	return "'" + strings.ReplaceAll(command, "'", `'"'"'`) + "'"
}

// unquoteShellArg reverses quoteShellArg so the command can be displayed.
func unquoteShellArg(quoted string) string {
	return strings.ReplaceAll(quoted, `'"'"'`, `'`)
}

// rewriteWrapperEcho replaces every wrapper echo with the plain command it
// carries, so the terminal shows the command instead of the internal syntax.
// Output that followed the echo on the same line is kept on its own line.
func rewriteWrapperEcho(line string) string {
	matches := evalWrapperEcho.FindAllStringSubmatchIndex(line, -1)
	if len(matches) == 0 {
		return line
	}
	var b strings.Builder
	prev := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		b.WriteString(line[prev:start])
		b.WriteString(unquoteShellArg(line[match[2]:match[3]]))
		rest := line[end:]
		next := end
		if strings.HasPrefix(rest, "\r") {
			// Drop the carriage return the shell echoed for the wrapper line.
			rest = rest[1:]
			next = end + 1
			if strings.TrimSpace(rest) != "" {
				b.WriteString("\n")
			}
		} else if strings.TrimSpace(rest) != "" {
			b.WriteString("\n")
		}
		prev = next
	}
	b.WriteString(line[prev:])
	return b.String()
}

type ExecutionResult struct {
	RequestID string `json:"requestId"`
	SessionID string `json:"sessionId"`
	Output    string `json:"output"`
	ExitCode  *int   `json:"exitCode"`
	Completed bool   `json:"completed"`
	Truncated bool   `json:"truncated"`
}

// ExecuteCommand runs in the visible POSIX shell, retaining cwd and environment.
// A unique framed marker, not quiet output, determines completion.
func (m *Manager) ExecuteCommand(ctx context.Context, id, command string) (string, error) {
	result := ExecutionResult{RequestID: uuid.NewString(), SessionID: id}
	pack := func(err error) (string, error) { data, _ := json.Marshal(result); return string(data), err }
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if strings.TrimSpace(command) == "" || strings.ContainsAny(command, "\x00\r\n") {
		return pack(errors.New("one non-interactive POSIX command is required"))
	}
	state, err := m.get(id)
	if err != nil {
		return pack(err)
	}
	if !state.execMu.TryLock() {
		return pack(ErrSessionBusy)
	}
	defer state.execMu.Unlock()
	state.mu.Lock()
	closed := state.closed
	offset := state.base + int64(len(state.output))
	// A new execution supersedes a stale "uncertain" flag left behind by a
	// previous timeout: the completion marker below uniquely identifies this run.
	state.uncertain = false
	state.mu.Unlock()
	if closed {
		return pack(ErrSessionClosed)
	}
	if err := ctx.Err(); err != nil {
		return pack(err)
	}
	marker := "__OO_DONE_" + strings.ReplaceAll(result.RequestID, "-", "") + ":"
	// eval runs in the current shell, unlike sh -c or a new SSH exec channel.
	quoted := quoteShellArg(command)
	wire := "\x15eval " + quoted + "; printf '\\036" + marker + "%d\\037\\n' \"$?\"\r"
	if _, err = state.stdin.Write([]byte(wire)); err != nil {
		return pack(err)
	}
	markUncertain := func() { state.mu.Lock(); state.uncertain = true; state.mu.Unlock() }
	var collected string
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			markUncertain()
			result.Output = ansiSequence.ReplaceAllString(collected, "")
			return pack(ctx.Err())
		case <-ticker.C:
			state.mu.Lock()
			base := state.base
			state.mu.Unlock()
			if offset < base {
				result.Truncated = true
			}
			out, readErr := m.rawOutput(id, offset)
			if readErr != nil {
				return pack(readErr)
			}
			offset = out.Offset
			collected += out.Data
			begin := strings.Index(collected, "\x1e"+marker)
			if begin >= 0 {
				tail := collected[begin+1+len(marker):]
				end := strings.IndexByte(tail, 0x1f)
				if end >= 0 {
					code, parseErr := strconv.Atoi(tail[:end])
					if parseErr == nil {
						result.ExitCode = &code
						result.Completed = true
						text := collected[:begin]
						// Remove the wrapper echo: either Ctrl-U line or the full eval+printf line.
						// Pattern: \x15eval '<cmd>'; printf '\036...%d\037\n' "$?"
						textLines := strings.Split(text, "\n")
						var cleanedLines []string
						for _, line := range textLines {
							// Skip Ctrl-U line (starts with \x15) and wrapper echoes containing eval/printf/marker
							if strings.Contains(line, "\x15") || strings.Contains(line, "\x1e") ||
								strings.Contains(line, marker) ||
								(strings.Contains(line, "eval") && strings.Contains(line, "printf")) {
								continue
							}
							cleanedLines = append(cleanedLines, line)
						}
						text = strings.Join(cleanedLines, "\n")
						result.Output = ansiSequence.ReplaceAllString(text, "")
						return pack(nil)
					}
				}
			}
			if len(collected) > 128*1024 {
				collected = collected[len(collected)-128*1024:]
				result.Truncated = true
			}
			if out.Closed {
				result.Output = ansiSequence.ReplaceAllString(collected, "")
				return pack(ErrSessionClosed)
			}
		}
	}
}
func (m *Manager) Resize(id string, cols, rows int) error {
	if cols < 1 || cols > 500 || rows < 1 || rows > 300 {
		return errors.New("invalid terminal dimensions")
	}
	state, err := m.get(id)
	if err != nil {
		return err
	}
	state.mu.Lock()
	closed, term := state.closed, state.ssh
	state.mu.Unlock()
	if closed {
		return ErrSessionClosed
	}
	return term.WindowChange(rows, cols)
}

func (m *Manager) CloseSession(id string) error {
	state, err := m.get(id)
	if err != nil {
		return err
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		return nil
	}
	state.closed = true
	state.err = ""
	state.mu.Unlock()
	_ = state.ssh.Close()
	_ = state.client.Close()
	return nil
}

func (m *Manager) get(id string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.sessions[id]
	if state == nil {
		return nil, ErrSessionNotFound
	}
	return state, nil
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.cancel()
	states := make([]*session, 0, len(m.sessions))
	for _, state := range m.sessions {
		states = append(states, state)
	}
	m.sessions = make(map[string]*session)
	m.mu.Unlock()
	for _, state := range states {
		state.mu.Lock()
		state.closed = true
		state.mu.Unlock()
		if state.ssh != nil {
			_ = state.ssh.Close()
		}
		if state.client != nil {
			_ = state.client.Close()
		}
	}
}

// scrubTerminalOutput removes the internal wrapper syntax from terminal output.
// The wrapper echo is rewritten to the plain command the AI ran, so the user
// can see what the AI is executing, and the completion marker is removed.
func scrubTerminalOutput(data string) string {
	if data == "" {
		return data
	}
	lines := strings.Split(data, "\n")
	var filtered []string
	for _, line := range lines {
		// Rewrite the wrapper echo into the plain command, e.g.
		//   eval 'ls -la'; printf '\036__OO_DONE_<id>:%d\037\n' "$?"
		// becomes
		//   ls -la
		// so the terminal shows the command instead of internal syntax.
		line = rewriteWrapperEcho(line)
		// Strip the completion marker frame. A line that only carried the marker
		// disappears; output sharing the line with the marker is preserved.
		stripped := markerFrame.ReplaceAllString(line, "")
		if stripped != line && strings.TrimSpace(stripped) == "" {
			continue
		}
		line = stripped
		// Remove control characters used in wrapper protocol
		cleaned := strings.ReplaceAll(line, "\x15", "")
		cleaned = strings.ReplaceAll(cleaned, "\x1e", "")
		cleaned = strings.ReplaceAll(cleaned, "\x1f", "")
		// Skip lines that contain wrapper command fragments
		if strings.Contains(cleaned, "printf") && (strings.Contains(cleaned, "\\036") || strings.Contains(cleaned, "\036")) {
			continue
		}
		if strings.Contains(cleaned, "eval ") && strings.Contains(cleaned, "'") {
			continue
		}
		// Skip marker-only lines (any line containing __OO_DONE_)
		if markerOnly.MatchString(cleaned) || strings.Contains(cleaned, "__OO_DONE_") {
			continue
		}
		filtered = append(filtered, cleaned)
	}
	return strings.Join(filtered, "\n")
}
