// Command gateway serves the CodeGateway-compatible HTTP API via go-zero rest,
// driving jarvis's agentcore/runtime for the web/ React frontend.
//
//	go run ./cmd/gateway -f etc/gateway.yaml
//
// Then: cd web && npm run dev  (proxies /v1 → :8080)
package main

import "github.com/alex6xu/jarvisserver/internal/gatewayapp"

func main() {
	gatewayapp.Main()
}
