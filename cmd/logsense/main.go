package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"logsense/internal/config"
	"logsense/internal/version"
	"logsense/internal/ui"
	"logsense/internal/util/logx"
)

func main() {
	logx.SetLevelFromEnv()
    cfg, err := config.Load()
    if err != nil {
        fmt.Fprintln(os.Stderr, "config error:", err)
        os.Exit(1)
    }

    if cfg.ShowVersion {
        fmt.Println("logsense", version.String())
        return
    }

	// Setup cancellation on SIGINT/SIGTERM
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Run TUI app
    logx.Infof("starting logsense %s: %s", version.String(), cfg.String())
	if err := ui.Run(ctx, cfg); err != nil {
		logx.Errorf("logsense exited with error: %v", err)
		os.Exit(1)
	}
}
