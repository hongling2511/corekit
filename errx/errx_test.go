package errx_test

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/hongling2511/corekit/errx"
)

// Codes registered by these tests use service 99 so they never collide with
// the library's 10xxxx codes.
var errWidgetNotFound = errx.Register(990101, http.StatusNotFound, "widget not found")

func TestTranslateRegisteredCode(t *testing.T) {
	got := errx.Translate(errWidgetNotFound)

	assert.Equal(t, 990101, got.Code())
	assert.Equal(t, http.StatusNotFound, got.HTTPStatus())
	assert.Equal(t, "widget not found", got.Message())
}

func TestWrapKeepsRegisteredMessageAndRootCause(t *testing.T) {
	root := fs.ErrNotExist
	err := fmt.Errorf("load widget: %w", errWidgetNotFound.Wrap(root, "widget id=42"))

	got := errx.Translate(err)

	assert.Equal(t, 990101, got.Code())
	assert.Equal(t, "widget not found", got.Message(), "callers see only the registered message")
	assert.ErrorIs(t, err, root, "the root cause stays in the chain")
	assert.ErrorIs(t, err, errWidgetNotFound)
	assert.Contains(t, err.Error(), "widget id=42", "the internal detail is kept for logs")
	assert.Contains(t, err.Error(), root.Error())
	assert.Equal(t, root, errors.Unwrap(errors.Unwrap(err)))
}

func TestWrapWithoutCause(t *testing.T) {
	err := errWidgetNotFound.Wrap(nil, "widget id=7")

	assert.ErrorIs(t, err, errWidgetNotFound)
	assert.Equal(t, "widget not found", errx.Translate(err).Message())
	assert.Contains(t, err.Error(), "widget id=7")
}

func TestTranslateUnregisteredErrorIsInternalWithoutDetail(t *testing.T) {
	err := fmt.Errorf("query users: %w", errors.New("sql: password=hunter2 near SELECT"))

	got := errx.Translate(err)

	assert.Equal(t, 100001, got.Code())
	assert.Equal(t, http.StatusInternalServerError, got.HTTPStatus())
	assert.Equal(t, "internal error", got.Message())
	assert.Same(t, errx.ErrInternal, got)
}

func TestTranslateNilIsNil(t *testing.T) {
	assert.Nil(t, errx.Translate(nil))
}

func TestLibraryCodes(t *testing.T) {
	tests := []struct {
		code       *errx.Code
		want       int
		httpStatus int
	}{
		{errx.ErrInternal, 100001, http.StatusInternalServerError},
		{errx.ErrBind, 100002, http.StatusBadRequest},
		{errx.ErrUnauthenticated, 100003, http.StatusUnauthorized},
		{errx.ErrPermissionDenied, 100004, http.StatusForbidden},
		{errx.ErrDatabase, 100005, http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.code.Message(), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.code.Code())
			assert.Equal(t, tt.httpStatus, tt.code.HTTPStatus())
			assert.NotEmpty(t, tt.code.Message())
		})
	}
}

func TestRegisterDuplicateCodePanics(t *testing.T) {
	assert.PanicsWithValue(t, "errx: error code 990101 is already registered", func() {
		_ = errx.Register(990101, http.StatusConflict, "another widget error")
	})
	assert.Panics(t, func() { _ = errx.Register(100001, http.StatusInternalServerError, "internal error") },
		"a library code cannot be registered again")
}

func TestRegisterRejectsMalformedCodes(t *testing.T) {
	tests := []struct {
		desc       string
		code       int
		httpStatus int
	}{
		{"five digits", 99999, http.StatusBadRequest},
		{"seven digits", 1000000, http.StatusBadRequest},
		{"negative", -990102, http.StatusBadRequest},
		{"success status", 990102, http.StatusOK},
		{"not an HTTP status", 990102, 600},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			assert.Panics(t, func() { _ = errx.Register(tt.code, tt.httpStatus, "bad") })
		})
	}
	assert.NotPanics(t, func() { _ = errx.Register(990102, http.StatusBadRequest, "good") },
		"rejected registrations leave the code free")
}

func TestGRPCCodeDerivedFromHTTPStatus(t *testing.T) {
	// Expected values are google.golang.org/grpc/codes numbers, following the
	// HTTP mapping documented in google/rpc/code.proto.
	tests := []struct {
		httpStatus int
		want       uint32
	}{
		{http.StatusBadRequest, 3},           // InvalidArgument
		{http.StatusUnauthorized, 16},        // Unauthenticated
		{http.StatusForbidden, 7},            // PermissionDenied
		{http.StatusNotFound, 5},             // NotFound
		{http.StatusConflict, 6},             // AlreadyExists
		{http.StatusPreconditionFailed, 9},   // FailedPrecondition
		{http.StatusTooManyRequests, 8},      // ResourceExhausted
		{499, 1},                             // Canceled (client closed request)
		{http.StatusInternalServerError, 13}, // Internal
		{http.StatusNotImplemented, 12},      // Unimplemented
		{http.StatusServiceUnavailable, 14},  // Unavailable
		{http.StatusGatewayTimeout, 4},       // DeadlineExceeded
		{http.StatusTeapot, 2},               // Unknown: no mapping
		{http.StatusBadGateway, 2},           // Unknown: no mapping
	}
	for i, tt := range tests {
		t.Run(http.StatusText(tt.httpStatus), func(t *testing.T) {
			c := errx.Register(990200+i, tt.httpStatus, "grpc mapping")

			assert.Equal(t, tt.want, c.GRPCCode())
		})
	}
}
