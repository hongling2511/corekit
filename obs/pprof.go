package obs

import (
	"bufio"
	"bytes"
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

// deltaProfiles are the profiles whose ?seconds=N delta is meaningful.
var deltaProfiles = map[string]bool{
	"allocs": true, "block": true, "goroutineleak": true, "goroutine": true,
	"heap": true, "mutex": true, "threadcreate": true,
}

// pprofDelta collects p, waits sec seconds, collects it again and answers
// with the difference.
func pprofDelta(w http.ResponseWriter, r *http.Request, p *pprof.Profile, sec string) {
	n, err := strconv.ParseInt(sec, 10, 64)
	switch {
	case err != nil || n <= 0:
		pprofError(w, http.StatusBadRequest, `invalid value for "seconds" - must be a positive integer`)
		return
	case !deltaProfiles[p.Name()]:
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
	select {
	case <-time.After(time.Duration(n) * time.Second):
	case <-r.Context().Done():
		pprofError(w, http.StatusInternalServerError, r.Context().Err().Error())
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
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, strings.Join(os.Args, "\x00"))
}

// seconds reads ?seconds=N (fractions allowed), falling back to def.
func seconds(r *http.Request, def float64) time.Duration {
	if n, err := strconv.ParseFloat(r.FormValue("seconds"), 64); err == nil && n > 0 {
		def = n
	}
	return time.Duration(def * float64(time.Second))
}

// collect runs start, waits d (or until the client leaves) and runs stop;
// the result is buffered so a failure can still answer with an error.
func collect(w http.ResponseWriter, r *http.Request, d time.Duration, start func(io.Writer) error, stop func()) {
	var buf bytes.Buffer
	if err := start(&buf); err != nil {
		http.Error(w, "could not start profiling: "+err.Error(), http.StatusInternalServerError)
		return
	}
	select {
	case <-time.After(d):
	case <-r.Context().Done():
	}
	stop()
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(buf.Bytes())
}

func pprofCPU(w http.ResponseWriter, r *http.Request) {
	collect(w, r, seconds(r, 30), pprof.StartCPUProfile, pprof.StopCPUProfile)
}

func pprofTrace(w http.ResponseWriter, r *http.Request) {
	collect(w, r, seconds(r, 1), trace.Start, trace.Stop)
}

// pprofSymbol answers GET with the symbol table's presence and POST, a
// "+"-separated list of hex program counters, with their function names.
func pprofSymbol(w http.ResponseWriter, r *http.Request) {
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
