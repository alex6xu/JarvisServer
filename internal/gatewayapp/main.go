// Package gatewayapp contains the shared process entry point for gateway commands.
package gatewayapp

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/alex6xu/jarvisserver/internal/distributedlog"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"

	"github.com/alex6xu/jarvisserver/internal/gateway"
)

const gatewayReadyTimeout = 15 * time.Second

// Main loads the gateway configuration and blocks while the HTTP server runs.
func Main() {
	configFile := flag.String("f", "etc/gateway.yaml", "config file")
	flag.Parse()
	if err := Run(*configFile); err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}
}

// Run starts the gateway with the configuration at configFile.
func Run(configFile string) error {
	return run(configFile, os.Stderr)
}

func run(configFile string, output io.Writer) error {
	configPath, err := filepath.Abs(configFile)
	if err != nil {
		configPath = configFile
	}
	startupLog(output, "loading configuration config=%q", configPath)

	var c gateway.Config
	if err := conf.Load(configFile, &c); err != nil {
		return fmt.Errorf("load config %s: %w", configFile, err)
	}
	startupLog(output, "configuration loaded service=%q mode=%q", c.Name, c.Mode)
	if err := logx.SetUp(c.Log); err != nil {
		return fmt.Errorf("configure logging: %w", err)
	}
	defer func() { _ = logx.Close() }()
	logx.DisableStat()
	startupLog(output, "logging configured mode=%q path=%q", c.Log.Mode, c.Log.Path)

	if c.Agent.Cwd != "" {
		if err := os.Chdir(c.Agent.Cwd); err != nil {
			return fmt.Errorf("chdir %s: %w", c.Agent.Cwd, err)
		}
		startupLog(output, "working directory changed path=%q", c.Agent.Cwd)
	}

	svc, err := gateway.NewService(c.ToOptions())
	if err != nil {
		return err
	}
	defer svc.Close()
	startupLog(output, "service initialized auth_mode=%q", svc.Opts.AuthMode)

	srv := gateway.NewServer(svc, c)
	defer srv.Stop()
	healthURL := gatewayHealthURL(c)
	startupLog(output, "starting pid=%d address=%s", os.Getpid(), strings.TrimSuffix(healthURL, "/healthz"))

	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		srv.Start()
	}()
	if err := waitForGatewayReady(healthURL, gatewayReadyTimeout, stopped); err != nil {
		return err
	}
	startupLog(output, "ready pid=%d health=%s", os.Getpid(), healthURL)
	svc.Logger.Info(context.Background(), "gateway ready",
		distributedlog.F("pid", os.Getpid()),
		distributedlog.F("health_url", healthURL),
	)
	<-stopped
	startupLog(output, "stopped pid=%d", os.Getpid())
	return nil
}

func startupLog(output io.Writer, format string, args ...any) {
	fmt.Fprintf(output, "%s gateway: %s\n", time.Now().Format(time.RFC3339), fmt.Sprintf(format, args...))
}

func gatewayHealthURL(c gateway.Config) string {
	host := strings.TrimSpace(c.Host)
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
	scheme := "http"
	if c.CertFile != "" && c.KeyFile != "" {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(c.Port)) + "/healthz"
}

func waitForGatewayReady(healthURL string, timeout time.Duration, stopped <-chan struct{}) error {
	client := &http.Client{Timeout: time.Second}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		resp, err := client.Get(healthURL)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-stopped:
			return fmt.Errorf("gateway stopped before becoming ready")
		case <-deadline.C:
			return fmt.Errorf("gateway readiness check timed out after %s: %s", timeout, healthURL)
		case <-ticker.C:
		}
	}
}
