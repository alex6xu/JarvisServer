// Command gateway serves the CodeGateway-compatible HTTP API via go-zero rest,
// driving jarvis's agentcore/runtime for the web/ React frontend.
//
//	go run ./cmd/gateway -f etc/gateway.yaml
//
// Then: cd web && npm run dev  (proxies /v1 → :8080)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/logx"

	"github.com/alex6xu/jarvisserver/internal/gateway"
)

var configFile = flag.String("f", "etc/gateway.yaml", "config file")

func main() {
	flag.Parse()

	var c gateway.Config
	conf.MustLoad(*configFile, &c)
	logx.MustSetup(c.Log)
	logx.DisableStat()

	if c.Agent.Cwd != "" {
		if err := os.Chdir(c.Agent.Cwd); err != nil {
			fmt.Fprintf(os.Stderr, "gateway: chdir %s: %v\n", c.Agent.Cwd, err)
			os.Exit(1)
		}
	}

	opts := c.ToOptions()
	svc, err := gateway.NewService(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gateway: %v\n", err)
		os.Exit(1)
	}

	srv := gateway.NewServer(svc, c)
	defer srv.Stop()
	srv.Start()
}
