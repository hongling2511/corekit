package obs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/pflag"

	"github.com/hongling2511/corekit/version"
)

// AdminOptions is the management server options block. Embed it in an
// options object under the key "admin"; its flags are --admin.addr and
// --admin.pprof and --admin.readiness_timeout.
type AdminOptions struct {
	Addr  string `mapstructure:"addr"`  // listen address, host:port; keep it off the gateway
	PProf bool   `mapstructure:"pprof"` // serve /debug/pprof
	// ReadinessTimeout bounds each /readyz run; a check still running then
	// counts as failed. Keep it below the probe's own timeout.
	ReadinessTimeout time.Duration `mapstructure:"readiness_timeout"`
}

// NewAdminOptions returns the default management options: :8081, pprof on,
// a 1s readiness timeout.
func NewAdminOptions() *AdminOptions {
	return &AdminOptions{Addr: ":8081", PProf: true, ReadinessTimeout: time.Second}
}

// AddFlags declares the block's flags on fs.
func (o *AdminOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&o.Addr, "admin.addr", o.Addr, "management listen address, host:port (probes, metrics, pprof, version)")
	fs.BoolVar(&o.PProf, "admin.pprof", o.PProf, "serve /debug/pprof on the management port")
	fs.DurationVar(&o.ReadinessTimeout, "admin.readiness_timeout", o.ReadinessTimeout,
		"upper bound of the readiness checks of one /readyz request, such as 1s")
}

// Complete does nothing; the block has no derived fields.
func (o *AdminOptions) Complete() error { return nil }

// Validate reports every invalid value, naming its key.
func (o *AdminOptions) Validate() error {
	var errs []error
	if o.Addr == "" {
		errs = append(errs, errors.New("admin.addr: must not be empty"))
	}
	if o.ReadinessTimeout <= 0 {
		errs = append(errs, fmt.Errorf("admin.readiness_timeout: invalid value %s, want a positive duration", o.ReadinessTimeout))
	}
	return errors.Join(errs...)
}

// Admin is the management server component, and the http.Handler it
// serves. It serves, on a port of its own:
//
//   - /livez: the process is alive, always 200;
//   - /readyz: the dependencies are ready, 200 when every readiness check
//     passes within the readiness timeout, otherwise 503 naming each failed
//     check (the reasons go to the log); 503 "shutting down" once the stop
//     has begun (see Drain);
//   - /metrics: the metrics, in the Prometheus text format;
//   - /version: the build information, as the version command prints it;
//   - /debug/pprof: the runtime profiles, unless turned off.
//
// It never requests itself. Importing obs leaves http.DefaultServeMux
// untouched.
type Admin struct {
	addr             string
	readinessTimeout time.Duration
	srv              *http.Server
	checks           []readinessCheck
	shuttingDown     atomic.Bool // set by Drain's Stop: /readyz answers 503 from then on
}

type readinessCheck struct {
	name  string
	check func(ctx context.Context) error
}

// NewAdmin returns a management server configured by opts that exposes
// metrics on /metrics.
func NewAdmin(opts *AdminOptions, metrics *Metrics) *Admin {
	a := &Admin{addr: opts.Addr, readinessTimeout: opts.ReadinessTimeout}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /readyz", a.readyz)
	mux.Handle("GET /metrics", metrics.handler())
	mux.HandleFunc("GET /version", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, version.Get().String())
	})
	if opts.PProf {
		addPprof(mux)
	}
	a.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return a
}

// AddReadinessCheck registers a check /readyz runs on every request, with
// a context that ends at the readiness timeout; any check returning an
// error, panicking or not returning in time, makes the process not ready.
// A check must return once its context is done: one that does not keeps
// its goroutine, on every probe. Register checks at start-up, before Start.
func (a *Admin) AddReadinessCheck(name string, check func(ctx context.Context) error) {
	a.checks = append(a.checks, readinessCheck{name: name, check: check})
}

func (a *Admin) readyz(w http.ResponseWriter, r *http.Request) {
	if a.shuttingDown.Load() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "shutting down\n")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.readinessTimeout)
	defer cancel()
	// Each check runs on its own, so one that ignores ctx cannot hold the
	// probe past the deadline.
	results := make([]chan error, len(a.checks))
	for i, c := range a.checks {
		results[i] = make(chan error, 1)
		go func() { results[i] <- runCheck(ctx, c.check) }()
	}
	var failed strings.Builder
	for i, c := range a.checks {
		var err error
		select {
		case err = <-results[i]: // a result that is in keeps it, even past the deadline
		default:
			select {
			case err = <-results[i]:
			case <-ctx.Done():
				err = ctx.Err()
			}
		}
		if err == nil {
			continue
		}
		state := "failed"
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() != nil {
			state = "timed out"
		}
		if r.Context().Err() == nil { // the prober is gone: nothing to report
			// The reason may hold addresses or queries: it goes only to the log.
			slog.WarnContext(r.Context(), "readiness check failed", "check", c.name, "state", state, "error", err.Error())
		}
		fmt.Fprintf(&failed, "%s: %s\n", c.name, state)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if failed.Len() > 0 {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, failed.String())
		return
	}
	_, _ = io.WriteString(w, "ok\n")
}

// runCheck runs check, turning a panic into its failure.
func runCheck(ctx context.Context, check func(ctx context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return check(ctx)
}

// Drain returns the component that begins the stop. Run it last, so it
// stops first: its Stop makes /readyz answer 503 "shutting down" at once,
// then waits window, or until its context is done, while every other
// component keeps serving, so load balancers take the process out of
// rotation before it stops taking requests.
func (a *Admin) Drain(window time.Duration) *Drain {
	return &Drain{admin: a, window: window}
}

// Drain is the component Admin.Drain returns.
type Drain struct {
	admin  *Admin
	window time.Duration
}

// Name returns "drain".
func (d *Drain) Name() string { return "drain" }

// Start does nothing until ctx is cancelled.
func (d *Drain) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// Stop reports the process not ready and waits the drain window.
func (d *Drain) Stop(ctx context.Context) error {
	d.admin.shuttingDown.Store(true)
	slog.InfoContext(ctx, "draining", "window", d.window.String())
	t := time.NewTimer(d.window)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
	return nil
}

// ServeHTTP serves one management request.
func (a *Admin) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.srv.Handler.ServeHTTP(w, r) }

// Name returns "admin".
func (a *Admin) Name() string { return "admin" }

// Start listens on the configured address and serves until Stop, or until
// ctx is cancelled, which closes the server at once.
func (a *Admin) Start(ctx context.Context) error {
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", a.addr)
	if err != nil {
		return err
	}
	slog.InfoContext(ctx, "admin server listening", "addr", ln.Addr().String())
	served := make(chan error, 1)
	go func() { served <- a.srv.Serve(ln) }()
	select {
	case err = <-served:
	case <-ctx.Done():
		_ = a.srv.Close()
		err = <-served
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Stop stops accepting connections and waits for the requests in flight to
// finish, giving up when ctx is done.
func (a *Admin) Stop(ctx context.Context) error { return a.srv.Shutdown(ctx) }
