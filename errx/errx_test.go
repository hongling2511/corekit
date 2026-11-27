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
}

// grpcCases are registered once at package level, so the tests can run
// repeatedly in one process (go test -count=N).
//
// Expected values are google.golang.org/grpc/codes numbers, following the
// HTTP mapping documented in google/rpc/code.proto.
var grpcCases = []struct {
	code *errx.Code
	want uint32
}{
	{errx.Register(990201, http.StatusBadRequest, "grpc mapping"), 3},           // InvalidArgument
	{errx.Register(990202, http.StatusUnauthorized, "grpc mapping"), 16},        // Unauthenticated
	{errx.Register(990203, http.StatusForbidden, "grpc mapping"), 7},            // PermissionDenied
	{errx.Register(990204, http.StatusNotFound, "grpc mapping"), 5},             // NotFound
	{errx.Register(990205, http.StatusConflict, "grpc mapping"), 6},             // AlreadyExists
	{errx.Register(990206, http.StatusPreconditionFailed, "grpc mapping"), 9},   // FailedPrecondition
	{errx.Register(990207, http.StatusTooManyRequests, "grpc mapping"), 8},      // ResourceExhausted
	{errx.Register(990208, 499, "grpc mapping"), 1},                             // Canceled (client closed request)
	{errx.Register(990209, http.StatusInternalServerError, "grpc mapping"), 13}, // Internal
	{errx.Register(990210, http.StatusNotImplemented, "grpc mapping"), 12},      // Unimplemented
	{errx.Register(990211, http.StatusServiceUnavailable, "grpc mapping"), 14},  // Unavailable
	{errx.Register(990212, http.StatusGatewayTimeout, "grpc mapping"), 4},       // DeadlineExceeded
	{errx.Register(990213, http.StatusTeapot, "grpc mapping"), 2},               // Unknown: no mapping
	{errx.Register(990214, http.StatusBadGateway, "grpc mapping"), 2},           // Unknown: no mapping
}

func TestGRPCCodeDerivedFromHTTPStatus(t *testing.T) {
	for _, tt := range grpcCases {
		t.Run(http.StatusText(tt.code.HTTPStatus()), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.code.GRPCCode())
		})
	}
}

func TestRegisterReservesLibraryRange(t *testing.T) {
	assert.PanicsWithValue(t, "errx: error code 109999 is in 10xxxx, which is reserved for the library; business codes start at 110000", func() {
		_ = errx.Register(109999, http.StatusBadRequest, "reserved")
	})
	assert.Equal(t, 110000, errFirstBusinessCode.Code(), "business codes start at 110000")
}

var errFirstBusinessCode = errx.Register(110000, http.StatusBadRequest, "first business code")

func TestMarkdownListsLibraryCodes(t *testing.T) {
	md := errx.Markdown()

	assert.Contains(t, md, "| 100001 | 500 | 13 | internal error |\n")
	assert.Contains(t, md, "| 100005 | 500 | 13 | database error |\n")
}

func TestTranslateUnregisteredCodeValueIsInternal(t *testing.T) {
	var nilCode *errx.Code
	tests := map[string]error{
		"zero Code":                       new(errx.Code),
		"nil *Code in a non-nil error":    nilCode,
		"zero Code wrapped":               fmt.Errorf("outer: %w", new(errx.Code)),
		"zero Code's Wrap":                new(errx.Code).Wrap(nil, "detail"),
		"nil *Code wrapped by fmt.Errorf": fmt.Errorf("outer: %w", nilCode),
	}
	for desc, err := range tests {
		t.Run(desc, func(t *testing.T) {
			assert.Same(t, errx.ErrInternal, errx.Translate(err))
		})
	}
}

var errMultiline = errx.Register(990301, http.StatusBadRequest, "line one\r\nline two\nline | three")

func TestMarkdownKeepsEachCodeOnOneLine(t *testing.T) {
	assert.Contains(t, errx.Markdown(), "| 990301 | 400 | 3 | line one line two line \\| three |\n")
}
