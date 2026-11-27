package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/hongling2511/corekit/errx"
	"github.com/hongling2511/corekit/httpx"
)

// demoRoutesEnv, read by serve alone, adds routes that demonstrate how the
// server answers an unregistered error, a panic and a slow request. It is
// not a configuration key.
const demoRoutesEnv = "SAMPLE_DEMO_ROUTES"

// demoRoutes reports whether the demo routes are on.
func demoRoutes() (bool, error) {
	v := os.Getenv(demoRoutesEnv)
	if v == "" {
		return false, nil
	}
	on, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: invalid value %q, want true or false", demoRoutesEnv, v)
	}
	return on, nil
}

func addDemoRoutes(r gin.IRouter) {
	r.GET("/demo/error", func(c *gin.Context) {
		httpx.Fail(c, errors.New("dial tcp 10.0.0.7:3306: connection refused"))
	})
	r.GET("/demo/panic", func(*gin.Context) {
		panic("demo panic")
	})
	// /demo/late?fail=error|panic writes part of a response, then fails.
	r.GET("/demo/late", func(c *gin.Context) {
		c.String(http.StatusOK, "partial")
		c.Writer.Flush()
		if c.Query("fail") == "panic" {
			panic("demo late panic")
		}
		httpx.Fail(c, errors.New("demo late failure"))
	})
	// /demo/slow?wait=300ms answers after that long.
	r.GET("/demo/slow", func(c *gin.Context) {
		wait, err := time.ParseDuration(c.Query("wait"))
		if err != nil {
			httpx.Fail(c, errx.ErrBind.Wrap(err, "wait"))
			return
		}
		slog.InfoContext(c.Request.Context(), "demo slow request waiting", "wait", wait.String())
		select {
		case <-time.After(wait):
		case <-c.Request.Context().Done():
			return // the client is gone
		}
		httpx.OK(c, gin.H{"waited": wait.String()})
	})
}
