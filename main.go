// Command jarvisserver starts the HTTP gateway from the repository root.
//
//	go run . -f etc/gateway.yaml
package main

import "github.com/alex6xu/jarvisserver/internal/gatewayapp"

func main() {
	gatewayapp.Main()
}
