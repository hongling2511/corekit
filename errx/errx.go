// Package errx is the error code system of corekit. An error code pairs a
// six-digit business code with an HTTP status and the message shown to
// callers; it is declared once with Register.
//
// A code has six digits: service (2) + module (2) + sequence (2). The
// library's own codes use 10xxxx; business projects start at 11xxxx.
//
// The registry is package-level state: besides logging, it is the only
// global state the library holds. Register codes only at start-up, in
// package-level variable declarations or while main assembles the program,
// never while serving requests.
package errx

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
)

// The library's error codes.
var (
	ErrInternal         = register(100001, http.StatusInternalServerError, "internal error")
	ErrBind             = register(100002, http.StatusBadRequest, "invalid request parameters")
	ErrUnauthenticated  = register(100003, http.StatusUnauthorized, "unauthenticated")
	ErrPermissionDenied = register(100004, http.StatusForbidden, "permission denied")
	ErrDatabase         = register(100005, http.StatusInternalServerError, "database error")
)

// Code is a registered error code. It is itself an error, so a registered
// code can be returned as is.
type Code struct {
	code       int
	httpStatus int
	message    string
}

// Code returns the six-digit business code.
func (c *Code) Code() int { return c.code }

// HTTPStatus returns the HTTP status registered with the code.
func (c *Code) HTTPStatus() int { return c.httpStatus }

// Message returns the message shown to callers.
func (c *Code) Message() string { return c.message }

// GRPCCode returns the gRPC status code derived from the HTTP status, as a
// google.golang.org/grpc/codes value, following the HTTP mapping in
// google/rpc/code.proto. A status without a mapping yields Unknown (2).
func (c *Code) GRPCCode() uint32 {
	if g, ok := grpcCodes[c.httpStatus]; ok {
		return g
	}
	return 2 // Unknown
}

var grpcCodes = map[int]uint32{
	http.StatusBadRequest:          3,  // InvalidArgument
	http.StatusUnauthorized:        16, // Unauthenticated
	http.StatusForbidden:           7,  // PermissionDenied
	http.StatusNotFound:            5,  // NotFound
	http.StatusConflict:            6,  // AlreadyExists
	http.StatusPreconditionFailed:  9,  // FailedPrecondition
	http.StatusTooManyRequests:     8,  // ResourceExhausted
	499:                            1,  // Canceled (client closed request)
	http.StatusInternalServerError: 13, // Internal
	http.StatusNotImplemented:      12, // Unimplemented
	http.StatusServiceUnavailable:  14, // Unavailable
	http.StatusGatewayTimeout:      4,  // DeadlineExceeded
}

func (c *Code) Error() string { return fmt.Sprintf("%d %s", c.code, c.message) }

// Wrap returns an error carrying this code, an internal detail and the
// cause, which may be nil. The detail and the cause appear in Error, for
// logs; Translate still yields only the registered message. errors.Is
// matches both the code and the cause.
func (c *Code) Wrap(cause error, detail string) error {
	return &wrapped{code: c, detail: detail, cause: cause}
}

func (c *Code) errxCode() *Code { return c }

type wrapped struct {
	code   *Code
	detail string
	cause  error
}

func (w *wrapped) Error() string {
	s := w.code.Error() + ": " + w.detail
	if w.cause != nil {
		s += ": " + w.cause.Error()
	}
	return s
}

func (w *wrapped) Unwrap() error        { return w.cause }
func (w *wrapped) Is(target error) bool { return target == w.code }
func (w *wrapped) errxCode() *Code      { return w.code }

var (
	mu       sync.Mutex
	registry = map[int]*Code{}
)

// Register declares an error code with the HTTP status (4xx or 5xx) and the
// message shown to callers. Declare codes as package-level variables:
// registering a code twice, a code that is not six digits, or a code in the
// library's 10xxxx range is a programming error and panics, so the program
// fails at start-up.
func Register(code, httpStatus int, message string) *Code {
	if code >= 100000 && code < 110000 {
		panic(fmt.Sprintf("errx: error code %d is in 10xxxx, which is reserved for the library; business codes start at 110000", code))
	}
	return register(code, httpStatus, message)
}

// register declares a code in any range; the library's own codes use it.
func register(code, httpStatus int, message string) *Code {
	if code < 100000 || code > 999999 {
		panic(fmt.Sprintf("errx: error code %d is not six digits", code))
	}
	if httpStatus < 400 || httpStatus > 599 {
		panic(fmt.Sprintf("errx: error code %d: HTTP status %d is not 4xx or 5xx", code, httpStatus))
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[code]; ok {
		panic(fmt.Sprintf("errx: error code %d is already registered", code))
	}
	c := &Code{code: code, httpStatus: httpStatus, message: message}
	registry[code] = c
	return c
}

// Translate returns the error code nearest the top of err's chain. An error
// without a registered code (including a zero or nil Code) translates to
// ErrInternal, so its text never
// reaches callers. Translate(nil) is nil.
func Translate(err error) *Code {
	if err == nil {
		return nil
	}
	var c interface{ errxCode() *Code }
	if errors.As(err, &c) && registered(c.errxCode()) {
		return c.errxCode()
	}
	return ErrInternal
}

// registered reports whether c was returned by Register, as opposed to a
// zero or nil Code.
func registered(c *Code) bool {
	if c == nil {
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	return registry[c.code] == c
}

// markdownCell keeps a message inside one table cell on one line.
var markdownCell = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "|", `\|`)

// Markdown renders every registered error code as a Markdown table, sorted
// by code, one line per code.
func Markdown() string {
	mu.Lock()
	codes := make([]*Code, 0, len(registry))
	for _, c := range registry {
		codes = append(codes, c)
	}
	mu.Unlock()
	slices.SortFunc(codes, func(a, b *Code) int { return cmp.Compare(a.code, b.code) })

	var b strings.Builder
	b.WriteString("# Error codes\n\n")
	b.WriteString("| Code | HTTP status | gRPC code | Message |\n")
	b.WriteString("| --- | --- | --- | --- |\n")
	for _, c := range codes {
		fmt.Fprintf(&b, "| %d | %d | %d | %s |\n", c.code, c.httpStatus, c.GRPCCode(), markdownCell.Replace(c.message))
	}
	return b.String()
}
