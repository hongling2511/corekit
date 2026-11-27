package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/hongling2511/corekit/errx"
	"github.com/hongling2511/corekit/log"
)

// envelope is the body of every response.
type envelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
	TraceID string `json:"trace_id"`
}

// listData is the data of a list response.
type listData[T any] struct {
	Items []T `json:"items"`
	Total int `json:"total"`
}

// OK answers with 200 and the success envelope carrying data.
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{Code: 0, Message: "OK", Data: data, TraceID: log.TraceID(c.Request.Context())})
}

// List answers with 200 and the success envelope whose data is
// {"items": [...], "total": n}; total is the size of the whole collection.
func List[T any](c *gin.Context, items []T, total int) {
	if items == nil {
		items = []T{}
	}
	OK(c, listData[T]{Items: items, Total: total})
}

// Fail answers with the failure envelope for err: the error code errx
// translates it to gives the HTTP status, code and message; data is null.
// An error without a registered code answers as errx.ErrInternal. The
// error itself, with its internal details, goes only to the log. When the
// response has already been written, Fail only logs.
func Fail(c *gin.Context, err error) {
	if err == nil {
		err = errors.New("httpx.Fail called with a nil error")
	}
	code := errx.Translate(err)
	ctx := c.Request.Context()
	level := slog.LevelWarn
	if code.HTTPStatus() >= http.StatusInternalServerError {
		level = slog.LevelError
	}
	slog.Log(ctx, level, "request failed", "code", code.Code(), "error", err.Error())
	if c.Writer.Written() {
		c.Abort() // too late for an envelope
		return
	}
	c.AbortWithStatusJSON(code.HTTPStatus(), envelope{Code: code.Code(), Message: code.Message(), Data: nil, TraceID: log.TraceID(ctx)})
}

// Bind decodes the request into obj (by its Content-Type, with gin's binding
// and validation tags). A JSON body must be exactly one JSON document,
// optionally followed by whitespace. A failure is errx.ErrBind wrapping the
// cause, ready for Fail.
func Bind(c *gin.Context, obj any) error {
	if err := bind(c, obj); err != nil {
		return errx.ErrBind.Wrap(err, "bind request")
	}
	return nil
}

func bind(c *gin.Context, obj any) error {
	if binding.Default(c.Request.Method, c.ContentType()) != binding.JSON {
		return c.ShouldBind(obj)
	}
	if c.Request.Body == nil {
		return errors.New("empty request body")
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var doc json.RawMessage
	if err := dec.Decode(&doc); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("unexpected data after the JSON document")
	}
	return binding.JSON.BindBody(body, obj)
}
