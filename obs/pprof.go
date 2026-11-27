package obs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"runtime"
	"runtime/pprof"
	"runtime/trace"
	"strconv"
	"strings"
	"time"

	"github.com/google/pprof/profile"
)

// The pprof endpoints follow net/http/pprof's paths, parameters and
// semantics (Go 1.26), so `go tool pprof` works against them, but are built
// on runtime/pprof and github.com/google/pprof/profile:
// importing net/http/pprof would register its handlers on
// http.DefaultServeMux, changing every program that imports obs.
//
// One difference remains: github.com/google/pprof/profile keeps a
// profile's default sample type when it writes a delta, which the
// standard library's copy drops, so `go tool pprof` shows an allocs delta
// as alloc_space here and as inuse_space against net/http/pprof. Pass
// -sample_index explicitly to compare the two.
func addPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/{$}", pprofIndex)
	mux.HandleFunc("GET /debug/pprof/{name}", pprofProfile)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprofCmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprofCPU)
	mux.HandleFunc("GET /debug/pprof/trace", pprofTrace)
	mux.HandleFunc("GET /debug/pprof/symbol", pprofSymbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprofSymbol)
}

func pprofIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	var b strings.Builder
	b.WriteString("<html><head><title>/debug/pprof/</title></head><body><p>Profiles:</p><table>\n")
	for _, p := range pprof.Profiles() {
		name := html.EscapeString(p.Name())
		fmt.Fprintf(&b, "<tr><td>%d</td><td><a href=\"%s?debug=1\">%s</a></td></tr>\n", p.Count(), name, name)
	}
	b.WriteString("<tr><td></td><td><a href=\"cmdline\">cmdline</a></td></tr>\n")
	b.WriteString("<tr><td></td><td><a href=\"profile\">profile</a> (CPU, ?seconds=30)</td></tr>\n")
	b.WriteString("<tr><td></td><td><a href=\"trace\">trace</a> (?seconds=1)</td></tr>\n")
	b.WriteString("</table></body></html>\n")
	_, _ = io.WriteString(w, b.String())
}

// pprofProfile serves a named runtime profile (heap, goroutine, allocs,
// block, mutex, threadcreate) as net/http/pprof does: ?debug=N selects the
// text form, ?gc=N with N > 0 collects garbage before a heap profile, and
// ?seconds=N returns the delta over the next N seconds.
func pprofProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	name := r.PathValue("name")
	p := pprof.Lookup(name)
	if p == nil {
		pprofError(w, http.StatusNotFound, "Unknown profile")
		return
	}
	if sec := r.FormValue("seconds"); sec != "" {
		pprofDelta(w, r, p, sec)
		return
	}
	if gc, _ := strconv.Atoi(r.FormValue("gc")); name == "heap" && gc > 0 {
		runtime.GC()
	}
	debug, _ := strconv.Atoi(r.FormValue("debug"))
	if debug != 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	}
	_ = p.WriteTo(w, debug)
}

// supportsDelta reports whether the ?seconds=N delta of a profile is
// meaningful.
func supportsDelta(name string) bool {
	switch name {
	case "allocs", "block", "goroutineleak", "goroutine", "heap", "mutex", "threadcreate":
		return true
	}
	return false
}

// pprofDelta collects p, waits sec seconds, collects it again and answers
// with the difference.
func pprofDelta(w http.ResponseWriter, r *http.Request, p *pprof.Profile, sec string) {
	n, err := strconv.ParseInt(sec, 10, 64)
	switch {
	case err != nil || n <= 0:
		pprofError(w, http.StatusBadRequest, `invalid value for "seconds" - must be a positive integer`)
		return
	case !supportsDelta(p.Name()):
		pprofError(w, http.StatusBadRequest, `"seconds" parameter is not supported for this profile type`)
		return
	}
	if debug, _ := strconv.Atoi(r.FormValue("debug")); debug != 0 {
		pprofError(w, http.StatusBadRequest, "seconds and debug params are incompatible")
		return
	}
	p0, err := collectProfile(p)
	if err != nil {
		pprofError(w, http.StatusInternalServerError, "failed to collect profile")
		return
	}
	t := time.NewTimer(time.Duration(n) * time.Second)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
		status := http.StatusInternalServerError
		if errors.Is(r.Context().Err(), context.DeadlineExceeded) {
			status = http.StatusRequestTimeout
		}
		pprofError(w, status, r.Context().Err().Error())
		return
	}
	p1, err := collectProfile(p)
	if err != nil {
		pprofError(w, http.StatusInternalServerError, "failed to collect profile")
		return
	}
	ts, dur := p1.TimeNanos, p1.TimeNanos-p0.TimeNanos
	p0.Scale(-1)
	delta, err := profile.Merge([]*profile.Profile{p0, p1})
	if err != nil {
		pprofError(w, http.StatusInternalServerError, "failed to compute delta")
		return
	}
	delta.TimeNanos, delta.DurationNanos = ts, dur
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+p.Name()+`-delta"`)
	_ = delta.Write(w)
}

func collectProfile(p *pprof.Profile) (*profile.Profile, error) {
	var buf bytes.Buffer
	if err := p.WriteTo(&buf, 0); err != nil {
		return nil, err
	}
	ts := time.Now().UnixNano()
	parsed, err := profile.Parse(&buf)
	if err != nil {
		return nil, err
	}
	parsed.TimeNanos = ts
	return parsed, nil
}

// pprofError answers with a plain-text error, marked as coming from pprof
// so `go tool pprof` shows it.
func pprofError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Go-Pprof", "1")
	w.Header().Del("Content-Disposition")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, msg+"\n")
}

func pprofCmdline(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, strings.Join(os.Args, "\x00"))
}

// pprofSleep waits d, or until the client leaves.
func pprofSleep(r *http.Request, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-r.Context().Done():
	}
}

// pprofCPU serves a CPU profile over ?seconds=N (an integer, default 30).
func pprofCPU(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	sec, err := strconv.ParseInt(r.FormValue("seconds"), 10, 64)
	if sec <= 0 || err != nil {
		sec = 30
	}
	// Set assuming StartCPUProfile works, because then it starts writing.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="profile"`)
	if err := pprof.StartCPUProfile(w); err != nil {
		pprofError(w, http.StatusInternalServerError, "Could not enable CPU profiling: "+err.Error())
		return
	}
	pprofSleep(r, time.Duration(sec)*time.Second)
	pprof.StopCPUProfile()
}

// pprofTrace serves an execution trace over ?seconds=N (fractions allowed,
// default 1).
func pprofTrace(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	sec, err := strconv.ParseFloat(r.FormValue("seconds"), 64)
	if sec <= 0 || err != nil {
		sec = 1
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="trace"`)
	if err := trace.Start(w); err != nil {
		pprofError(w, http.StatusInternalServerError, "Could not enable tracing: "+err.Error())
		return
	}
	pprofSleep(r, time.Duration(sec*float64(time.Second)))
	trace.Stop()
}

// pprofSymbol answers GET with the symbol table's presence and POST, a
// "+"-separated list of hex program counters, with their function names.
func pprofSymbol(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	var b strings.Builder
	b.WriteString("num_symbols: 1\n")
	var in *bufio.Reader
	if r.Method == http.MethodPost {
		in = bufio.NewReader(r.Body)
	} else {
		in = bufio.NewReader(strings.NewReader(r.URL.RawQuery))
	}
	for {
		word, err := in.ReadSlice('+')
		if err == nil {
			word = word[:len(word)-1]
		}
		if pc, perr := strconv.ParseUint(string(word), 0, 64); perr == nil && pc != 0 {
			if f := runtime.FuncForPC(uintptr(pc)); f != nil {
				fmt.Fprintf(&b, "%#x %s\n", pc, f.Name())
			}
		}
		if err != nil {
			break
		}
	}
	_, _ = io.WriteString(w, b.String())
}
