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
		Data:   scrubTerminalOutput(string(state.output[start:readEnd])),
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
	if !state.execMu.TryLock() {
		return ErrSessionBusy
	}
	defer state.execMu.Unlock()
	state.mu.Lock()
	closed, stdin, uncertain := state.closed, state.stdin, state.uncertain
	state.mu.Unlock()
	if uncertain {
		return ErrExecutionUncertain
	}
	if closed {
		return ErrSessionClosed
	}
	_, err = stdin.Write(data)
	return err
}

var ErrSessionBusy = errors.New("terminal is executing an AI command")
var ErrExecutionUncertain = errors.New("previous execution did not finish; reconnect before sending more commands")
var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var markerOnly = regexp.MustCompile(`__OO_DONE_[a-z0-9]+:`)

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
	closed, uncertain := state.closed, state.uncertain
	offset := state.base + int64(len(state.output))
	state.mu.Unlock()
	if closed {
		return pack(ErrSessionClosed)
	}
	if uncertain {
		return pack(ErrExecutionUncertain)
	}
	if err := ctx.Err(); err != nil {
		return pack(err)
	}
	marker := "__OO_DONE_" + strings.ReplaceAll(result.RequestID, "-", "") + ":"
	// eval runs in the current shell, unlike sh -c or a new SSH exec channel.
	quoted := "'" + strings.ReplaceAll(command, "'", "'\"'\"'") + "'"
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
			out, readErr := m.Output(id, offset)
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

// scrubTerminalOutput removes wrapper commands and completion markers from terminal output.
// Filters: Ctrl-U lines, eval/printf wrapper echoes, and __OO_DONE_ markers.
// scrubTerminalOutput removes wrapper commands and completion markers from terminal output.
// Filters: Ctrl-U lines, eval/printf wrapper echoes, and __OO_DONE_ markers.
func scrubTerminalOutput(data string) string {
	if data == "" {
		return data
	}
	lines := strings.Split(data, "\n")
	var filtered []string
	for _, line := range lines {
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
		// Skip lines that are empty or whitespace after control char removal
		if strings.TrimSpace(cleaned) == "" {
			if len(filtered) == 0 || filtered[len(filtered)-1] != "" {
				filtered = append(filtered, "")
			}
			continue
		}
		filtered = append(filtered, cleaned)
	}
	// Trim leading and trailing empty lines
	for len(filtered) > 0 && filtered[0] == "" {
		filtered = filtered[1:]
	}
	for len(filtered) > 0 && filtered[len(filtered)-1] == "" {
		filtered = filtered[:len(filtered)-1]
	}
	return strings.Join(filtered, "\n")
}