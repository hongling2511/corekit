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
	"github.com/hongling2511/corekit/obs"
)

// Server is an HTTP server component. It is also the http.Handler it
// serves.
type Server struct {
	opts    Options
	srv     *http.Server
	metrics *obs.HTTPMetrics
}

// New returns a server configured by opts whose routes are declared by
// routes. Every route, and the answer to an unmatched one, runs behind the
// fixed middleware chain Recovery → Tracing → AccessLog → Metrics, which
// cannot be turned off or reordered, and then behind the optional
// middlewares, in order (see middleware.Registry).
func New(opts *Options, routes func(r gin.IRouter), optional ...gin.HandlerFunc) *Server {
	engine := gin.New()
	metrics := obs.NewHTTPMetrics()
	engine.Use(middleware.Recovery(Fail), middleware.Tracing(), middleware.AccessLog(), middleware.Metrics(metrics))
	engine.Use(optional...)
	// No automatic redirects (/users/ → /users, case or path fixes): such a
	// path is unmatched and answers with the envelope like any other.
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.HandleMethodNotAllowed = true
	engine.NoRoute(func(c *gin.Context) { Fail(c, errx.ErrRouteNotFound) })
	engine.NoMethod(func(c *gin.Context) { Fail(c, errx.ErrMethodNotAllowed) })
	routes(engine)
	return &Server{
		opts:    *opts,
		metrics: metrics,
		srv: &http.Server{
			Handler:      engine,
			ReadTimeout:  opts.ReadTimeout,
			WriteTimeout: opts.WriteTimeout,
			IdleTimeout:  opts.IdleTimeout,
		},
	}
}

// ServeHTTP serves one request through the fixed chain and the routes.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.srv.Handler.ServeHTTP(w, r) }

// Metrics returns what the server has recorded per method, route and status.
func (s *Server) Metrics() []obs.HTTPRequestStats { return s.metrics.Snapshot() }

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
