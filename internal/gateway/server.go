package gateway

import (
	"fmt"

	"github.com/zeromicro/go-zero/rest"
)

// Server wraps a go-zero rest.Server around the existing Service.
type Server struct {
	Svc  *Service
	Rest *rest.Server
	Cfg  Config
}

// NewServer builds a go-zero rest.Server with CodeGateway-compatible routes.
func NewServer(svc *Service, cfg Config) *Server {
	rs := rest.MustNewServer(cfg.RestConf, serverRunOptions()...)
	s := &Server{Svc: svc, Rest: rs, Cfg: cfg}
	registerMiddlewares(s.Rest, s.Svc)
	registerRoutes(s.Rest, s.Svc)
	return s
}

// Start blocks on the go-zero server.
func (s *Server) Start() {
	fmt.Printf("gateway listening on %s:%d (model=%s approve=%v)\n",
		s.Cfg.Host, s.Cfg.Port, s.Svc.Opts.Model, s.Svc.Opts.Approve)
	s.Rest.Start()
}

// Stop shuts down the go-zero server.
func (s *Server) Stop() {
	s.Rest.Stop()
}
