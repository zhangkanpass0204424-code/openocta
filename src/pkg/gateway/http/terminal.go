package http

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	gatewayterminal "github.com/openocta/openocta/pkg/gateway/terminal"
)

const maxTerminalRequestBody = 64 << 10

type terminalSessionRequest struct {
	Host               string      `json:"host"`
	Port               int         `json:"port,omitempty"`
	Username           string      `json:"username"`
	Password           secretBytes `json:"password,omitempty"`
	PrivateKey         secretBytes `json:"privateKey,omitempty"`
	Passphrase         secretBytes `json:"passphrase,omitempty"`
	HostKeyFingerprint string      `json:"hostKeyFingerprint,omitempty"`
}

type secretBytes []byte

func (s *secretBytes) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return errors.New("credential must be a string")
	}
	*s = append((*s)[:0], value...)
	return nil
}

func (s *secretBytes) clear() {
	for i := range *s {
		(*s)[i] = 0
	}
	*s = nil
}

type terminalInputRequest struct {
	Data string `json:"data"`
}

type terminalResizeRequest struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}

func decodeTerminalJSON(w http.ResponseWriter, r *http.Request, target interface{}) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxTerminalRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request must contain one JSON value")
		}
		return err
	}
	return nil
}

func validateTerminalSessionRequest(req terminalSessionRequest) (gatewayterminal.ConnectRequest, error) {
	if req.Host != strings.TrimSpace(req.Host) || !validSSHHost(req.Host) {
		return gatewayterminal.ConnectRequest{}, errors.New("host is required and must be a hostname or IP address")
	}
	if req.Port == 0 {
		req.Port = 22
	}
	if req.Port < 1 || req.Port > 65535 {
		return gatewayterminal.ConnectRequest{}, errors.New("port must be between 1 and 65535")
	}
	if req.Username == "" || len(req.Username) > 256 || strings.TrimSpace(req.Username) != req.Username {
		return gatewayterminal.ConnectRequest{}, errors.New("username is required")
	}
	for _, r := range req.Username {
		if unicode.IsControl(r) {
			return gatewayterminal.ConnectRequest{}, errors.New("username contains invalid characters")
		}
	}
	if req.HostKeyFingerprint != "" && len(req.Password) == 0 && len(req.PrivateKey) == 0 {
		return gatewayterminal.ConnectRequest{}, errors.New("password or privateKey is required")
	}
	if len(req.Passphrase) != 0 && len(req.PrivateKey) == 0 {
		return gatewayterminal.ConnectRequest{}, errors.New("passphrase requires privateKey")
	}
	if len(req.PrivateKey) > maxTerminalRequestBody || len(req.Password) > maxTerminalRequestBody || len(req.Passphrase) > maxTerminalRequestBody {
		return gatewayterminal.ConnectRequest{}, errors.New("credentials are too large")
	}
	return gatewayterminal.ConnectRequest{
		Host:               req.Host,
		Port:               req.Port,
		Username:           req.Username,
		Password:           req.Password,
		PrivateKey:         req.PrivateKey,
		Passphrase:         req.Passphrase,
		HostKeyFingerprint: req.HostKeyFingerprint,
	}, nil
}

func validSSHHost(host string) bool {
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\\t\r\n[]") {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	if strings.HasSuffix(host, ".") {
		host = strings.TrimSuffix(host, ".")
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-') {
				return false
			}
		}
	}
	return true
}

func (s *Server) handleTerminalSessionCreate(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	if s.terminalHostStore == nil {
		writeTerminalError(w, http.StatusServiceUnavailable, "terminal_store_unavailable")
		return
	}
	var body terminalSessionRequest
	defer body.Password.clear()
	defer body.PrivateKey.clear()
	defer body.Passphrase.clear()
	if err := decodeTerminalJSON(w, r, &body); err != nil {
		writeTerminalDecodeError(w, err)
		return
	}

	req, err := validateTerminalSessionRequest(body)
	if err != nil {
		writeTerminalError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id, fingerprint, err := s.terminalManager.Connect(r.Context(), req)
	switch {
	case errors.Is(err, gatewayterminal.ErrHostKeyConfirmationRequired):
		writeTerminalJSON(w, http.StatusPreconditionFailed, map[string]string{
			"code":        "host_key_confirmation_required",
			"fingerprint": fingerprint,
		})
	case errors.Is(err, gatewayterminal.ErrHostKeyMismatch):
		writeTerminalError(w, http.StatusConflict, "host_key_mismatch")
	case errors.Is(err, gatewayterminal.ErrCapacity):
		writeTerminalError(w, http.StatusServiceUnavailable, "session_capacity_reached")
	case err != nil:
		writeTerminalError(w, http.StatusBadGateway, "ssh_connection_failed")
	default:
		host, saveErr := s.terminalHostStore.Upsert(gatewayterminal.SavedHost{
			Host:        req.Host,
			Port:        req.Port,
			Username:    req.Username,
			Fingerprint: fingerprint,
		})
		if saveErr != nil {
			_ = s.terminalManager.CloseSession(id)
			writeTerminalError(w, http.StatusInternalServerError, "terminal_host_save_failed")
			return
		}
		writeTerminalJSON(w, http.StatusCreated, map[string]string{
			"id": host.ID, "sessionId": id,
		})
	}
}

func (s *Server) handleTerminalHostsList(w http.ResponseWriter, _ *http.Request) {
	setSiteProxyCORSHeaders(w)
	if s.terminalHostStore == nil {
		writeTerminalError(w, http.StatusServiceUnavailable, "terminal_store_unavailable")
		return
	}
	writeTerminalJSON(w, http.StatusOK, s.terminalHostStore.List())
}

func (s *Server) handleTerminalHostDelete(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	if s.terminalHostStore == nil {
		writeTerminalError(w, http.StatusServiceUnavailable, "terminal_store_unavailable")
		return
	}
	if err := s.terminalHostStore.Remove(r.PathValue("id")); err != nil {
		if errors.Is(err, gatewayterminal.ErrHostNotFound) {
			writeTerminalError(w, http.StatusNotFound, "host_not_found")
			return
		}
		writeTerminalError(w, http.StatusInternalServerError, "terminal_host_delete_failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalSessionOutput(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	offset := int64(0)
	if raw := r.URL.Query().Get("offset"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			writeTerminalError(w, http.StatusBadRequest, "invalid_offset")
			return
		}
		offset = parsed
	}
	result, err := s.terminalManager.Output(r.PathValue("id"), offset)
	if err != nil {
		writeTerminalManagerError(w, err)
		return
	}
	response := map[string]interface{}{
		"data": result.Data, "offset": result.Offset, "closed": result.Closed,
	}
	if result.Error != "" {
		response["error"] = result.Error
	}
	writeTerminalJSON(w, http.StatusOK, response)
}

func (s *Server) handleTerminalSessionInput(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	var body terminalInputRequest
	if err := decodeTerminalJSON(w, r, &body); err != nil {
		writeTerminalDecodeError(w, err)
		return
	}
	if len(body.Data) > maxTerminalRequestBody {
		writeTerminalError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	if err := s.terminalManager.Input(r.PathValue("id"), []byte(body.Data)); err != nil {
		writeTerminalManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalSessionResize(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	var body terminalResizeRequest
	if err := decodeTerminalJSON(w, r, &body); err != nil {
		writeTerminalDecodeError(w, err)
		return
	}
	if body.Cols < 1 || body.Cols > 500 || body.Rows < 1 || body.Rows > 300 {
		writeTerminalError(w, http.StatusBadRequest, "invalid_dimensions")
		return
	}
	if err := s.terminalManager.Resize(r.PathValue("id"), body.Cols, body.Rows); err != nil {
		writeTerminalManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalSessionDelete(w http.ResponseWriter, r *http.Request) {
	setSiteProxyCORSHeaders(w)
	if err := s.terminalManager.CloseSession(r.PathValue("id")); err != nil {
		writeTerminalManagerError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTerminalOptions(w http.ResponseWriter, _ *http.Request) {
	setSiteProxyCORSHeaders(w)
	w.WriteHeader(http.StatusNoContent)
}

func writeTerminalManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gatewayterminal.ErrSessionNotFound):
		writeTerminalError(w, http.StatusNotFound, "session_not_found")
	case errors.Is(err, gatewayterminal.ErrSessionBusy), errors.Is(err, gatewayterminal.ErrExecutionUncertain):
		writeTerminalError(w, http.StatusConflict, "terminal_input_blocked")
	case errors.Is(err, gatewayterminal.ErrSessionClosed):
		writeTerminalError(w, http.StatusConflict, "session_closed")
	default:
		writeTerminalError(w, http.StatusBadGateway, "terminal_operation_failed")
	}
}

func writeTerminalError(w http.ResponseWriter, status int, code string) {
	writeTerminalJSON(w, status, map[string]string{"code": code})
}

func writeTerminalDecodeError(w http.ResponseWriter, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		writeTerminalError(w, http.StatusRequestEntityTooLarge, "request_too_large")
		return
	}
	writeTerminalError(w, http.StatusBadRequest, "invalid_request")
}

func writeTerminalJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
