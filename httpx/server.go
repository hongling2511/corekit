// Package httpx is corekit's HTTP server role: a lifecycle component that
// serves a gin router behind the fixed middleware chain, and the only two
// ways to answer a request, OK and Fail, which write the response envelope
// {code, message, data, trace_id}.
//
// gin.Context stops at the handler: a handler binds the request, calls the
// service layer with the request context (c.Request.Context()) and plain
// structs, and answers with OK, List or Fail.
//
// gin's mode is process-wide and left to the program: in its default debug
// mode gin prints route tables and warnings to stdout, so a program that
// logs JSON to stdout calls gin.SetMode(gin.ReleaseMode) in main.
package httpx

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/errx"
	"github.com/hongling2511/corekit/middleware"
)

// Server is an HTTP server component.
type Server struct {
	opts Options
	srv  *http.Server
}

// New returns a server configured by opts whose routes are declared by
// routes. Every route, and the answer to an unmatched one, runs behind the
// fixed middleware chain Recovery → Tracing → AccessLog → Metrics, which
// cannot be turned off or reordered.
func New(opts *Options, routes func(r gin.IRouter)) *Server {
	engine := gin.New()
	// Metrics has no recorder yet: the measurements are dropped until the
	// metrics exposure lands.
	engine.Use(middleware.Recovery(Fail), middleware.Tracing(), middleware.AccessLog(), middleware.Metrics(nil))
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(func(c *gin.Context) { Fail(c, errx.ErrRouteNotFound) })
	engine.NoMethod(func(c *gin.Context) { Fail(c, errx.ErrMethodNotAllowed) })
	routes(engine)
	return &Server{
		opts: *opts,
		srv: &http.Server{
			Handler:      engine,
			ReadTimeout:  opts.ReadTimeout,
			WriteTimeout: opts.WriteTimeout,
			IdleTimeout:  opts.IdleTimeout,
		},
	}
}

// Name returns "http".
func (s *Server) Name() string { return "http" }

// Start listens on the configured address and serves until Stop, or until
// ctx is cancelled, which closes the server at once.
func (s *Server) Start(ctx context.Context) error {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "http server listening", "addr", ln.Addr().String(), "tls", s.opts.TLS.Enabled())
	served := make(chan error, 1)
	go func() {
		if s.opts.TLS.Enabled() {
			served <- s.srv.ServeTLS(ln, s.opts.TLS.CertFile, s.opts.TLS.KeyFile)
			return
		}
		served <- s.srv.Serve(ln)
	}()
	select {
	case err = <-served:
	case <-ctx.Done():
		_ = s.srv.Close()
		err = <-served
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop stops accepting connections and waits for the requests in flight to
// finish, giving up when ctx is done.
func (s *Server) Stop(ctx context.Context) error { return s.srv.Shutdown(ctx) }
