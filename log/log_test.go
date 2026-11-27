package log_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hongling2511/corekit/log"
)

func decode(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var got map[string]any
	require.NoError(t, json.Unmarshal(line, &got))
	return got
}

func TestContextHandlerAddsTraceAndUserFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(log.NewContextHandler(slog.NewJSONHandler(&buf, nil)))
	ctx := log.WithUserID(log.WithTraceID(context.Background(), "4bf92f3577b34da6a3ce929d0e0e4736"), "u-42")

	logger.InfoContext(ctx, "hello", "k", "v")

	got := decode(t, buf.Bytes())
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", got["trace_id"])
	assert.Equal(t, "u-42", got["user_id"])
	assert.Equal(t, "v", got["k"])
}

func TestContextHandlerOmitsFieldsMissingFromContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(log.NewContextHandler(slog.NewJSONHandler(&buf, nil)))

	logger.InfoContext(context.Background(), "hello")

	got := decode(t, buf.Bytes())
	assert.NotContains(t, got, "trace_id")
	assert.NotContains(t, got, "user_id")
}

func TestContextHandlerKeepsWorkingAfterWith(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(log.NewContextHandler(slog.NewJSONHandler(&buf, nil))).With("svc", "a")
	ctx := log.WithTraceID(context.Background(), "t-1")

	logger.InfoContext(ctx, "hello")

	got := decode(t, buf.Bytes())
	assert.Equal(t, "a", got["svc"])
	assert.Equal(t, "t-1", got["trace_id"])
}

func TestOptionsDefaultsAreValid(t *testing.T) {
	opts := log.NewOptions()

	require.NoError(t, opts.Complete())
	require.NoError(t, opts.Validate())
	assert.Equal(t, log.Options{Level: "info", Format: "json", Output: "stdout"}, *opts)
}

func TestOptionsFlags(t *testing.T) {
	opts := log.NewOptions()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	opts.AddFlags(fs)

	require.NoError(t, fs.Parse([]string{"--log.level=debug", "--log.format=text", "--log.output=stderr"}))

	assert.Equal(t, log.Options{Level: "debug", Format: "text", Output: "stderr"}, *opts)
}

func TestOptionsCompleteNormalisesCase(t *testing.T) {
	opts := &log.Options{Level: "WARN", Format: "Text", Output: "STDERR"}

	require.NoError(t, opts.Complete())

	assert.Equal(t, log.Options{Level: "warn", Format: "text", Output: "stderr"}, *opts)
}

func TestOptionsValidateNamesTheBadKey(t *testing.T) {
	tests := []struct {
		opts log.Options
		want string
	}{
		{log.Options{Level: "loud", Format: "json", Output: "stdout"}, "log.level"},
		{log.Options{Level: "info", Format: "xml", Output: "stdout"}, "log.format"},
		{log.Options{Level: "info", Format: "json", Output: "/var/log/app.log"}, "log.output"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			err := tt.opts.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestContextHandlerKeepsFieldsTopLevelInGroups(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(log.NewContextHandler(slog.NewJSONHandler(&buf, nil))).With("svc", "a").WithGroup("req").With("id", 1)
	ctx := log.WithUserID(log.WithTraceID(context.Background(), "t-1"), "u-1")

	logger.InfoContext(ctx, "hello", "k", "v")

	got := decode(t, buf.Bytes())
	assert.Equal(t, "t-1", got["trace_id"])
	assert.Equal(t, "u-1", got["user_id"])
	assert.Equal(t, "a", got["svc"])
	assert.Equal(t, map[string]any{"id": float64(1), "k": "v"}, got["req"])
}

func TestNewCompletesBeforeValidating(t *testing.T) {
	logger, err := log.New(&log.Options{Level: "INFO", Format: "JSON", Output: "STDERR"})

	require.NoError(t, err)
	assert.NotNil(t, logger)
}
