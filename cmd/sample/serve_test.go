package main_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// logLine is one JSON log line of `serve`.
type logLine struct {
	Msg       string `json:"msg"`
	Component string `json:"component"`
	raw       string
}

// lockedBuffer is a strings.Builder safe to read while the process writes.
type lockedBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// serving is a running `sample serve` process.
type serving struct {
	cmd    *exec.Cmd
	lines  chan logLine // stdout log lines; closed at EOF
	waited chan error   // the result of cmd.Wait, once stdout is closed
	stderr *lockedBuffer
	seen   []logLine
}

// startServe starts `sample serve` with args and a clean environment plus env.
func startServe(t *testing.T, env []string, args ...string) *serving {
	t.Helper()
	cmd := exec.Command(sampleBinary(t), append([]string{"serve"}, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	s := &serving{cmd: cmd, lines: make(chan logLine, 100), waited: make(chan error, 1), stderr: &lockedBuffer{}}
	cmd.Stderr = s.stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		for range s.lines { // drain so the reader ends and Wait reaps the process
		}
	})
	go func() {
		defer close(s.lines)
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			l := logLine{raw: sc.Text()}
			_ = json.Unmarshal(sc.Bytes(), &l)
			s.lines <- l
		}
		_, _ = io.Copy(io.Discard, stdout)
		s.waited <- cmd.Wait() // Wait only after stdout is fully read
	}()
	return s
}

// waitFor reads log lines until one has msg and component (if not empty).
func (s *serving) waitFor(t *testing.T, msg, component string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-s.lines:
			if !ok {
				t.Fatalf("serve exited before logging %q %q; stderr: %s", msg, component, s.stderr)
			}
			s.seen = append(s.seen, l)
			if l.Msg == msg && (component == "" || l.Component == component) {
				return
			}
		case <-timeout:
			t.Fatalf("serve did not log %q %q", msg, component)
		}
	}
}

// exit waits for the process to end and returns its exit code and the
// component events ("msg component") it logged.
func (s *serving) exit(t *testing.T) (code int, events []string) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for lines := s.lines; lines != nil; {
		select {
		case l, ok := <-lines:
			if !ok {
				lines = nil
				break
			}
			s.seen = append(s.seen, l)
		case <-timeout:
			t.Fatal("serve did not exit")
		}
	}
	var err error
	select {
	case err = <-s.waited:
	case <-timeout:
		t.Fatal("serve did not exit")
	}
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	for _, l := range s.seen {
		if l.Component != "" {
			events = append(events, l.Msg+" "+l.Component)
		}
	}
	return code, events
}

func TestServeStopsInReverseOrderOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			s := startServe(t, nil)
			s.waitFor(t, "component starting", "second")

			require.NoError(t, s.cmd.Process.Signal(sig))
			code, events := s.exit(t)

			assert.Equal(t, 0, code, s.stderr.String())
			assert.Equal(t, []string{
				"component starting first",
				"component starting second",
				"component stopping second",
				"component stopped second",
				"component stopping first",
				"component stopped first",
			}, events)
		})
	}
}

func TestServeExitsWhenAComponentHangsPastTheShutdownTimeout(t *testing.T) {
	s := startServe(t, []string{"SAMPLE_DEMO_HANG=true"}, "--shutdown.timeout=200ms")
	s.waitFor(t, "component starting", "second")

	began := time.Now()
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	code, events := s.exit(t)

	assert.NotEqual(t, 0, code)
	assert.Less(t, time.Since(began), 5*time.Second)
	assert.Contains(t, s.stderr.String(), "stop timed out after 200ms")
	assert.Equal(t, []string{
		"component starting first",
		"component starting second",
		"component stopping second",
	}, events)
}

func TestServeStopsTheOthersWhenAComponentFails(t *testing.T) {
	s := startServe(t, []string{"SAMPLE_DEMO_FAIL=100ms"})

	code, events := s.exit(t)

	assert.NotEqual(t, 0, code)
	assert.Contains(t, s.stderr.String(), "component second: demo failure")
	assert.Equal(t, []string{
		"component starting first",
		"component starting second",
		"component failed second",
		"component stopping first",
		"component stopped first",
	}, events)
}

func TestShutdownTimeoutMustBePositive(t *testing.T) {
	r := run(t, nil, "serve", "--shutdown.timeout=0s")

	assert.NotEqual(t, 0, r.code)
	assert.Contains(t, r.stderr, "shutdown.timeout")
}

func TestServeSecondSignalEndsTheProcessAtOnce(t *testing.T) {
	s := startServe(t, []string{"SAMPLE_DEMO_HANG=true"}, "--shutdown.timeout=1m")
	s.waitFor(t, "component starting", "second")

	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	s.waitFor(t, "component stopping", "second")
	began := time.Now()
	require.NoError(t, s.cmd.Process.Signal(syscall.SIGTERM))
	code, _ := s.exit(t)

	assert.Equal(t, -1, code, "killed by the signal")
	assert.Less(t, time.Since(began), 5*time.Second)
}

func TestInvalidDemoSettingsAreRejected(t *testing.T) {
	for _, env := range []string{"SAMPLE_DEMO_FAIL=-1s", "SAMPLE_DEMO_FAIL=soon", "SAMPLE_DEMO_HANG=maybe"} {
		t.Run(env, func(t *testing.T) {
			r := run(t, []string{env}, "serve")

			assert.NotEqual(t, 0, r.code)
			name, _, _ := strings.Cut(env, "=")
			assert.Contains(t, r.stderr, name)
		})
	}
}

func TestDemoSettingsDoNotAffectOperationsCommands(t *testing.T) {
	// The demo knobs belong to serve only: not flags, not configuration keys.
	for _, args := range [][]string{{"version", "--help"}, {"config", "print", "--help"}} {
		r := run(t, nil, args...)
		require.Equal(t, 0, r.code, r.stderr)
		assert.NotContains(t, r.stdout, "demo", args)
	}
	assert.NotContains(t, printedConfig(t, nil), "demo.fail")
	r := run(t, []string{"SAMPLE_DEMO_FAIL=-1s"}, "version")
	assert.Equal(t, 0, r.code, r.stderr)
}
