// Package desktop provides gateway startup logic for the Wails desktop app.
// Reuses paths, config, and gateway HTTP server from the CLI gateway command.
package desktop

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/openocta/openocta/pkg/config"
	gatewayhttp "github.com/openocta/openocta/pkg/gateway/http"
	"github.com/openocta/openocta/pkg/logging"
	"github.com/openocta/openocta/pkg/paths"
	"github.com/openocta/openocta/pkg/version"
)

const (
	// DesktopPort is the fixed port for the desktop app gateway.
	DesktopPort = 18900
)

// StartGateway starts the gateway server for desktop mode.
// Sets OPENOCTA_RUN_MODE=desktop, uses port 18900, and paths.ResolveStateDir.
// Returns the server (for shutdown) and any startup error.
func StartGateway() (*gatewayhttp.Server, error) {
	env := func(k string) string { return os.Getenv(k) }

	// Ensure desktop mode and port
	os.Setenv("OPENOCTA_RUN_MODE", "desktop")
	os.Setenv("OPENOCTA_GATEWAY_PORT", fmt.Sprintf("%d", DesktopPort))
	// 桌面模式跳过 Cron，减少后台定时任务带来的不稳定。
	// Channels（含企业微信 WebSocket）不再默认跳过，以便 App 内可用；若需关闭可手动设置 OPENOCTA_SKIP_CHANNELS=1。
	//os.Setenv("OPENOCTA_SKIP_CRON", "1")

	// Resolve state dir (used by config, gateway, and logs)
	stateDir := paths.ResolveStateDir(env)
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create state dir %s: %w", stateDir, err)
	}
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}

	// 确保默认配置存在（与 CLI 行为一致）
	if err := config.EnsureDefaultConfig(env); err != nil {
		// 非致命，仅记录
		_ = os.WriteFile(filepath.Join(stateDir, "desktop-startup.log"), []byte("init config: "+err.Error()+"\n"), 0600)
	}

	// Init global logger: console + rolling file in state dir
	os.Setenv("OPENOCTA_LOG_DIR", logDir)
	opts := logging.GlobalOpts{LogDir: logDir, Level: logging.LevelInfo, ConsoleLevel: logging.LevelTrace}
	logging.InitGlobal(logDir, opts)
	logging.RedirectStdLog(logging.LevelTrace)

	// Load config for run mode
	cfg, cfgErr := config.Load(env)
	if cfgErr != nil {
		logging.Warn("Gateway config load failed: %v", cfgErr)
	}
	var cfgMode *string
	bind := ""
	customBindHost := ""
	if cfg != nil && cfg.Gateway != nil {
		cfgMode = cfg.Gateway.Mode
		if cfg.Gateway.Bind != nil {
			bind = *cfg.Gateway.Bind
		}
		if cfg.Gateway.CustomBindHost != nil {
			customBindHost = *cfg.Gateway.CustomBindHost
		}
	}
	runMode := paths.ResolveRunMode(env, cfgMode)
	addr := paths.ResolveGatewayAddrWithBind(DesktopPort, runMode, bind, customBindHost)

	logging.Info("Desktop gateway starting addr=%s mode=%s version=%s", addr, runMode, version.Version)

	srv := gatewayhttp.NewServer(addr, version.Version)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != context.Canceled {
			logging.Error("Gateway error: %v", err)
		}
	}()

	return srv, nil
}

// WaitForHealthy polls TCP 127.0.0.1:DesktopPort until the gateway accepts connections or ctx is done.
// Uses raw TCP dial instead of HTTP to avoid sandbox/HTTP-client issues when launched from Finder.
func WaitForHealthy(ctx context.Context, timeout time.Duration) error {
	addr := fmt.Sprintf("127.0.0.1:%d", DesktopPort)
	deadline := time.Now().Add(timeout)
	// 首次检查前等待，确保 ListenAndServe 已绑定端口（Finder 启动时调度可能不同）
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(500 * time.Millisecond):
	}
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("gateway not healthy within %v (port %d may be in use, check ~/.openocta/logs/)", timeout, DesktopPort)
}
