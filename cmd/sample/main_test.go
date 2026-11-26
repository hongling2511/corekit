package main_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	buildOnce sync.Once
	binary    string
	buildErr  error
)

// sampleBinary builds the sample once with a plain `go build` (no -ldflags),
// so the tests exercise exactly what an operator would run. It runs inside
// the tests rather than TestMain because go test records the files a test
// opens (to invalidate its cache) only once the tests have started.
func sampleBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		if buildErr = touchModuleSources("../.."); buildErr != nil {
			return
		}
		binary = filepath.Join(tmpDir, "sample")
		build := exec.Command("go", "build", "-o", binary, ".")
		build.Stderr = os.Stderr
		buildErr = build.Run()
	})
	require.NoError(t, buildErr, "build the sample")
	return binary
}

var tmpDir string

func TestMain(m *testing.M) {
	var err error
	if tmpDir, err = os.MkdirTemp("", "corekit-sample-"); err != nil {
		panic(err)
	}
	code := m.Run()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

// touchModuleSources reads every Go source and go.mod/go.sum file of the
// module at root. go test caches results by the files a test opens, and the
// sample is built in a subprocess, so this makes library changes invalidate
// the cached result.
func touchModuleSources(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "bin") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(path, ".go") || d.Name() == "go.mod" || d.Name() == "go.sum") {
			_, err = os.ReadFile(path)
		}
		return err
	})
}

type result struct {
	code           int
	stdout, stderr string
}

// run executes the sample binary with args and a clean environment plus env.
func run(t *testing.T, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command(sampleBinary(t), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}, env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr := (*exec.ExitError)(nil); errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else {
		require.NoError(t, err)
	}
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func TestVersionPrintsEmbeddedBuildInfo(t *testing.T) {
	r := run(t, nil, "version")

	require.Equal(t, 0, r.code, r.stderr)
	// The independent source of truth is the toolchain's own reading of the
	// build information embedded in the binary.
	embedded := goVersionM(t)
	assert.Contains(t, r.stdout, "version: "+embedded["mod"][1]+"\n")
	assert.Contains(t, r.stdout, "path: github.com/hongling2511/corekit/cmd/sample\n")
	assert.Contains(t, r.stdout, "module: github.com/hongling2511/corekit\n")
	assert.Contains(t, r.stdout, "go: "+runtime.Version()+"\n")
	// VCS data is stamped by go build in a regular clone (as in CI) but not in
	// a linked git worktree; either way the output must match the binary.
	assert.Contains(t, r.stdout, "revision: "+embedded["vcs.revision"][0]+"\n")
	assert.Contains(t, r.stdout, "time: "+embedded["vcs.time"][0]+"\n")
	if os.Getenv("CI") == "true" {
		// GitHub Actions builds from a regular clone, so VCS data must be there.
		assert.Regexp(t, `(?m)^revision: [0-9a-f]{40}$`, r.stdout)
		assert.Regexp(t, `(?m)^version: v\d+\.\d+\.\d+\S*$`, r.stdout)
	}
	t.Logf("version output:\n%s", r.stdout)
}

// goVersionM returns the build information `go version -m` reports for the
// sample binary, keyed by the first field ("mod", "dep") or by build setting.
func goVersionM(t *testing.T) map[string][]string {
	t.Helper()
	out, err := exec.Command("go", "version", "-m", sampleBinary(t)).Output()
	require.NoError(t, err)
	info := map[string][]string{"vcs.revision": {""}, "vcs.time": {""}}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		switch {
		case len(fields) >= 2 && fields[0] == "build":
			if k, v, ok := strings.Cut(fields[1], "="); ok {
				info[k] = []string{v}
			}
		case len(fields) >= 2:
			info[fields[0]] = fields[1:]
		}
	}
	return info
}

// printedConfig runs `config print` and decodes its stdout, which must hold
// nothing but the configuration.
func printedConfig(t *testing.T, env []string, args ...string) map[string]any {
	t.Helper()
	r := run(t, env, append([]string{"config", "print"}, args...)...)
	require.Equal(t, 0, r.code, r.stderr)
	var got map[string]any
	require.NoError(t, json.Unmarshal([]byte(r.stdout), &got), r.stdout)
	return got
}

func TestConfigPrintShowsEffectiveConfig(t *testing.T) {
	got := printedConfig(t, nil)

	assert.Equal(t, map[string]any{
		"log.level":  "info",
		"log.format": "json",
		"log.output": "stdout",
		"token":      "",
	}, got)
}

func TestOpsCommandsLogStartupToStderr(t *testing.T) {
	// Operations subcommands keep stdout for their result, whatever
	// log.output says, so scripts can parse it.
	for _, output := range []string{"stdout", "stderr"} {
		t.Run("config print with log.output="+output, func(t *testing.T) {
			r := run(t, nil, "config", "print", "--log.output="+output)

			require.Equal(t, 0, r.code, r.stderr)
			dec := json.NewDecoder(strings.NewReader(r.stdout))
			var cfg map[string]any
			require.NoError(t, dec.Decode(&cfg), r.stdout)
			assert.False(t, dec.More(), "stdout holds only the configuration: %q", r.stdout)
			assert.Equal(t, output, cfg["log.output"])
			assert.Contains(t, r.stderr, `"msg":"starting"`)
		})
		t.Run("version with log.output="+output, func(t *testing.T) {
			r := run(t, nil, "version", "--log.output="+output)

			require.Equal(t, 0, r.code, r.stderr)
			assert.True(t, strings.HasPrefix(r.stdout, "version: "), r.stdout)
			assert.NotContains(t, r.stdout, "starting")
			assert.Contains(t, r.stderr, `"msg":"starting"`)
		})
	}
}

func TestConfigPrecedence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.yaml")
	require.NoError(t, os.WriteFile(file, []byte("log:\n  level: warn\n"), 0o600))
	tests := []struct {
		desc string
		env  []string
		args []string
		want string
	}{
		{desc: "default", want: "info"},
		{desc: "file beats default", args: []string{"--config", file}, want: "warn"},
		{desc: "env beats file", env: []string{"SAMPLE_LOG_LEVEL=error"}, args: []string{"--config", file}, want: "error"},
		{
			desc: "flag beats env", env: []string{"SAMPLE_LOG_LEVEL=error"},
			args: []string{"--config", file, "--log.level=debug"}, want: "debug",
		},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			assert.Equal(t, tt.want, printedConfig(t, tt.env, tt.args...)["log.level"])
		})
	}
}

func TestInvalidConfigExitsNonZeroNamingTheKey(t *testing.T) {
	tests := []struct {
		desc string
		env  []string
		args []string
		want string
	}{
		{desc: "bad flag value", args: []string{"--log.level=loud"}, want: "log.level"},
		{desc: "bad env value", env: []string{"SAMPLE_LOG_FORMAT=xml"}, want: "log.format"},
		{desc: "missing config file", args: []string{"--config", "/nonexistent/sample.yaml"}, want: "/nonexistent/sample.yaml"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			r := run(t, tt.env, append([]string{"version"}, tt.args...)...)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, tt.want)
			assert.Empty(t, r.stdout, "the subcommand must not run")
		})
	}
}

func TestCompleteRunsBeforeValidate(t *testing.T) {
	// log.Options.Complete lower-cases the format; Validate only accepts
	// lower case, so this passes only if Complete ran first.
	got := printedConfig(t, nil, "--log.format=TEXT")

	assert.Equal(t, "text", got["log.format"])
}

func TestStartupLogsVersionAndRedactedConfig(t *testing.T) {
	r := run(t, []string{"SAMPLE_TOKEN=s3cr3t-token"}, "version")

	require.Equal(t, 0, r.code, r.stderr)
	assert.NotContains(t, r.stdout+r.stderr, "s3cr3t-token")
	line, _, _ := strings.Cut(r.stderr, "\n")
	var entry struct {
		Level   string         `json:"level"`
		Msg     string         `json:"msg"`
		Version string         `json:"version"`
		Config  map[string]any `json:"config"`
	}
	require.NoError(t, json.Unmarshal([]byte(line), &entry), "first stderr line is the JSON start-up log: %q", line)
	assert.Equal(t, "INFO", entry.Level)
	assert.Equal(t, "starting", entry.Msg)
	assert.Equal(t, goVersionM(t)["mod"][1], entry.Version)
	assert.Equal(t, "******", entry.Config["token"])
	assert.Equal(t, "info", entry.Config["log.level"])
}

func TestLogFormatAndLevel(t *testing.T) {
	t.Run("text format", func(t *testing.T) {
		r := run(t, nil, "version", "--log.format=text")

		require.Equal(t, 0, r.code, r.stderr)
		assert.Regexp(t, `(?m)^time=\S+ level=INFO msg=starting app=sample`, r.stderr)
	})
	t.Run("level above info suppresses the start-up log", func(t *testing.T) {
		r := run(t, nil, "version", "--log.level=warn")

		require.Equal(t, 0, r.code, r.stderr)
		assert.Empty(t, r.stderr)
	})
}

func TestUnknownConfigFileKeyExitsNonZero(t *testing.T) {
	tests := []struct{ file, want string }{
		{"log:\n  leve1: error\n", "log.leve1"},
		{"log:\n  leve1: {}\n", "log.leve1"},
		{"bogus: {}\n", "bogus"},
		{"log.level.typo: debug\n", "log.level.typo"},
		{"log.leve1: debug\n", "log.leve1"},
	}
	for _, tt := range tests {
		t.Run(tt.want+" in "+strings.TrimSpace(tt.file), func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "sample.yaml")
			require.NoError(t, os.WriteFile(file, []byte(tt.file), 0o600))

			r := run(t, nil, "version", "--config", file)

			assert.NotEqual(t, 0, r.code)
			assert.Contains(t, r.stderr, tt.want)
			assert.Empty(t, r.stdout, "the subcommand must not run")
		})
	}
}

func TestEmptyEnvOverridesConfigFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sample.yaml")
	require.NoError(t, os.WriteFile(file, []byte("token: from-file\n"), 0o600))

	got := printedConfig(t, []string{"SAMPLE_TOKEN="}, "--config", file)

	assert.Equal(t, "", got["token"])
}
