// Package gateway exposes pigo's agentcore/runtime over HTTP for the CodeGateway
// React frontend (web/). The HTTP surface is served by go-zero rest.Server
// (thin route/middleware/config layer); chat/run/SSE business logic stays in
// Service / RunManager / translate.
//
// See docs/codegateway-web-integration.md and etc/gateway.yaml.
package gateway
