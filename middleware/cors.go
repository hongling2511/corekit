package middleware

import (
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/pflag"
)

// CORSOptions are the parameters of the optional middleware "cors", under
// the key middleware.cors.
type CORSOptions struct {
	AllowOrigins     []string      `mapstructure:"allow_origins"`     // origins allowed; "*" allows any
	AllowMethods     []string      `mapstructure:"allow_methods"`     // methods a preflight allows
	AllowHeaders     []string      `mapstructure:"allow_headers"`     // request headers a preflight allows
	ExposeHeaders    []string      `mapstructure:"expose_headers"`    // response headers the browser may read
	AllowCredentials bool          `mapstructure:"allow_credentials"` // allow cookies and credentials
	MaxAge           time.Duration `mapstructure:"max_age"`           // how long a preflight may be cached; 0 sends no Max-Age
}

func newCORSOptions() CORSOptions {
	return CORSOptions{
		AllowOrigins:  []string{},
		AllowMethods:  []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead},
		AllowHeaders:  []string{"Content-Type", "Authorization", "traceparent"},
		ExposeHeaders: []string{TraceIDHeader},
	}
}

func (o *CORSOptions) addFlags(fs *pflag.FlagSet) {
	fs.StringSliceVar(&o.AllowOrigins, "middleware.cors.allow_origins", o.AllowOrigins, `cors: allowed origins; "*" allows any`)
	fs.StringSliceVar(&o.AllowMethods, "middleware.cors.allow_methods", o.AllowMethods, "cors: methods a preflight allows")
	fs.StringSliceVar(&o.AllowHeaders, "middleware.cors.allow_headers", o.AllowHeaders, "cors: request headers a preflight allows")
	fs.StringSliceVar(&o.ExposeHeaders, "middleware.cors.expose_headers", o.ExposeHeaders, "cors: response headers the browser may read")
	fs.BoolVar(&o.AllowCredentials, "middleware.cors.allow_credentials", o.AllowCredentials, "cors: allow credentials")
	fs.DurationVar(&o.MaxAge, "middleware.cors.max_age", o.MaxAge, "cors: how long a preflight may be cached, such as 10m; 0 sends no Max-Age")
}

func (o *CORSOptions) validate() []error {
	var errs []error
	if o.AllowCredentials && slices.Contains(o.AllowOrigins, "*") {
		errs = append(errs, errors.New(`middleware.cors.allow_credentials: credentials cannot be allowed for origin "*"`))
	}
	if o.MaxAge < 0 {
		errs = append(errs, errors.New("middleware.cors.max_age: must not be negative"))
	}
	return errs
}

// CORS answers cross-origin requests from the allowed origins: it adds the
// CORS headers to every response to such a request and answers a preflight
// (OPTIONS with Access-Control-Request-Method) itself with 204. A request
// from another origin is served without CORS headers. With "*" the
// request's own origin is echoed (with Vary: Origin), so credentials stay
// possible for listed origins. max_age is sent in whole seconds.
func CORS(o CORSOptions) gin.HandlerFunc {
	anyOrigin := slices.Contains(o.AllowOrigins, "*")
	methods := strings.Join(o.AllowMethods, ", ")
	headers := strings.Join(o.AllowHeaders, ", ")
	expose := strings.Join(o.ExposeHeaders, ", ")
	maxAge := strconv.Itoa(int(o.MaxAge / time.Second))
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		c.Writer.Header().Add("Vary", "Origin")
		if origin == "" || !anyOrigin && !slices.Contains(o.AllowOrigins, origin) {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		if o.AllowCredentials {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		if c.Request.Method != http.MethodOptions || c.GetHeader("Access-Control-Request-Method") == "" {
			if expose != "" {
				h.Set("Access-Control-Expose-Headers", expose)
			}
			c.Next()
			return
		}
		h.Add("Vary", "Access-Control-Request-Method")
		h.Add("Vary", "Access-Control-Request-Headers")
		h.Set("Access-Control-Allow-Methods", methods)
		if headers != "" {
			h.Set("Access-Control-Allow-Headers", headers)
		}
		if o.MaxAge > 0 {
			h.Set("Access-Control-Max-Age", maxAge)
		}
		c.AbortWithStatus(http.StatusNoContent)
	}
}
