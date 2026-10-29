package paths

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	newStateDirname      = ".openocta"
	newStateDirnameWin   = "openocta"
	configFilename       = "openocta.json"
	defaultGatewayPort   = 18900
	legacyStateDirname   = ".clawdbot"
	legacyConfigClawdbot = "clawdbot.json"
)

// ResolveStateDir returns the state directory for mutable data (sessions, logs, caches).
// Override via OPENOCTA_STATE_DIR or CLAWDBOT_STATE_DIR.
// Default: ~/.openocta on Linux/macOS, %APPDATA%\openocta on Windows.
func ResolveStateDir(env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	override := strings.TrimSpace(env("OPENOCTA_STATE_DIR"))
	if override == "" {
		override = strings.TrimSpace(env("CLAWDBOT_STATE_DIR"))
	}
	if override != "" {
		return expandUserPath(override, env)
	}
	if runtime.GOOS == "windows" {
		appData := strings.TrimSpace(env("APPDATA"))
		if appData == "" {
			appData = strings.TrimSpace(env("LOCALAPPDATA"))
		}
		if appData == "" {
			home := resolveHomeDir(env)
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		newDir := filepath.Join(appData, newStateDirnameWin)
		legacyDir := filepath.Join(appData, legacyStateDirname)
		if pathExists(newDir) {
			return newDir
		}
		if pathExists(legacyDir) {
			return legacyDir
		}
		return newDir
	}
	home := resolveHomeDir(env)
	newDir := filepath.Join(home, newStateDirname)
	legacyDir := filepath.Join(home, legacyStateDirname)
	if pathExists(newDir) {
		return newDir
	}
	if pathExists(legacyDir) {
		return legacyDir
	}
	return newDir
}

// ResolveConfigPath returns the active config file path.
// Override via OPENOCTA_CONFIG_PATH or CLAWDBOT_CONFIG_PATH.
// Default: $STATE_DIR/openocta.json
func ResolveConfigPath(env func(string) string, stateDir string) string {
	if env == nil {
		env = os.Getenv
	}
	override := strings.TrimSpace(env("OPENOCTA_CONFIG_PATH"))
	if override == "" {
		override = strings.TrimSpace(env("CLAWDBOT_CONFIG_PATH"))
	}
	if override != "" {
		return expandUserPath(override, env)
	}
	candidates := []string{
		filepath.Join(stateDir, configFilename),
		filepath.Join(stateDir, legacyConfigClawdbot),
	}
	for _, c := range candidates {
		if pathExists(c) {
			return c
		}
	}
	return filepath.Join(stateDir, configFilename)
}

// ResolveCanonicalConfigPath returns the canonical config path regardless of existence.
func ResolveCanonicalConfigPath(env func(string) string, stateDir string) string {
	if env == nil {
		env = os.Getenv
	}
	override := strings.TrimSpace(env("OPENOCTA_CONFIG_PATH"))
	if override == "" {
		override = strings.TrimSpace(env("CLAWDBOT_CONFIG_PATH"))
	}
	if override != "" {
		return expandUserPath(override, env)
	}
	return filepath.Join(stateDir, configFilename)
}

// DefaultGatewayPort is the default gateway listen port.
func DefaultGatewayPort() int {
	return defaultGatewayPort
}

// ResolveGatewayPort returns the gateway port from config or env.
func ResolveGatewayPort(portFromConfig *int, env func(string) string) int {
	if env == nil {
		env = os.Getenv
	}
	envRaw := strings.TrimSpace(env("OPENOCTA_GATEWAY_PORT"))
	if envRaw == "" {
		envRaw = strings.TrimSpace(env("CLAWDBOT_GATEWAY_PORT"))
	}
	if envRaw != "" {
		var n int
		if _, err := fmt.Sscanf(envRaw, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	if portFromConfig != nil && *portFromConfig > 0 {
		return *portFromConfig
	}
	return defaultGatewayPort
}

// ResolveOAuthDir returns the OAuth credentials directory.
func ResolveOAuthDir(env func(string) string, stateDir string) string {
	if env == nil {
		env = os.Getenv
	}
	override := strings.TrimSpace(env("OPENOCTA_OAUTH_DIR"))
	if override != "" {
		return expandUserPath(override, env)
	}
	return filepath.Join(stateDir, "credentials")
}

func resolveHomeDir(env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	home := env("OPENOCTA_HOME")
	if home == "" {
		home = env("HOME")
	}
	if home == "" {
		home = env("USERPROFILE")
	}
	if home != "" {
		home = strings.TrimSpace(home)
		if strings.HasPrefix(home, "~") {
			base := env("HOME")
			if base == "" {
				base = env("USERPROFILE")
			}
			if base != "" {
				home = filepath.Join(base, strings.TrimPrefix(home, "~"))
			}
		}
		return filepath.Clean(home)
	}
	dir, err := os.UserHomeDir()
	if err == nil {
		return dir
	}
	return "."
}

func expandUserPath(input string, env func(string) string) string {
	if env == nil {
		env = os.Getenv
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return input
	}
	if strings.HasPrefix(input, "~") {
		home := resolveHomeDir(env)
		return filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(input, "~"), "/"))
	}
	abs, err := filepath.Abs(input)
	if err != nil {
		return input
	}
	return abs
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// ResolveRunMode resolves the gateway run mode: "desktop" or "service".
//
// Priority:
// 1) OPENOCTA_RUN_MODE (desktop|service)
// 2) gateway.mode in config (desktop|service|auto|local|remote)
// 3) platform default: darwin/windows => desktop, linux => service
func ResolveRunMode(env func(string) string, gatewayModeFromConfig *string) string {
	if env != nil {
		raw := strings.TrimSpace(env("OPENOCTA_RUN_MODE"))
		switch strings.ToLower(raw) {
		case "desktop":
			return "desktop"
		case "service":
			return "service"
		}
	}

	if gatewayModeFromConfig != nil {
		switch strings.ToLower(strings.TrimSpace(*gatewayModeFromConfig)) {
		case "desktop":
			return "desktop"
		case "service":
			return "service"
		case "local":
			// Back-compat: historical "local" means loopback-only.
			return "desktop"
		case "remote":
			// Back-compat: historical "remote" implies network-accessible.
			return "service"
		case "auto":
			// fall through to platform default
		}
	}

	switch runtime.GOOS {
	case "darwin", "windows":
		return "desktop"
	default:
		return "service"
	}
}

// ResolveGatewayAddr returns the listen address for the given port and run mode.
// - desktop: 127.0.0.1:port (loopback-only)
// - service: :port (all interfaces)
func ResolveGatewayAddr(port int, runMode string) string {
	return ResolveGatewayAddrWithBind(port, runMode, "", "")
}

// ResolveGatewayAddrWithBind resolves the listen address, honoring an explicit
// gateway bind setting while preserving run-mode defaults for auto/unspecified.
func ResolveGatewayAddrWithBind(port int, runMode, bind, customHost string) string {
	switch strings.ToLower(strings.TrimSpace(bind)) {
	case "lan":
		return fmt.Sprintf(":%d", port)
	case "custom":
		host := strings.TrimSpace(customHost)
		if host != "" {
			return net.JoinHostPort(host, fmt.Sprintf("%d", port))
		}
	}
	if strings.EqualFold(strings.TrimSpace(runMode), "desktop") {
		return fmt.Sprintf("127.0.0.1:%d", port)
	}
	return fmt.Sprintf(":%d", port)
}
