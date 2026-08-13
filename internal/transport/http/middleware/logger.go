// Package middleware holds the cross-cutting HTTP concerns: request logging and
// panic recovery.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/transport/http/response"
)

// RequestLogger emits one structured line per request, including any error a
// handler attached with c.Error.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", c.FullPath()),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("duration", time.Since(start)),
		}
		if err := c.Errors.Last(); err != nil {
			attrs = append(attrs, slog.String("error", err.Error()))
		}

		logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "request", attrs...)
	}
}

// Recovery turns a panic into a 500 in the service's error envelope, so a bug
// in one handler does not take the process down mid-settlement.
func Recovery(logger *slog.Logger) gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(nil, func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "handler panicked",
			"path", c.FullPath(), "panic", recovered)
		response.Error(c, http.StatusInternalServerError, "internal error")
		c.Abort()
	})
}
