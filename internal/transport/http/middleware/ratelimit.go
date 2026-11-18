package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/domain/ratelimit"
	"github.com/islu/x402-facilitator/internal/transport/http/response"
)

// RateLimit caps requests per caller IP. It bounds what the per-payer quota
// cannot: forged or junk payloads, each of which still costs an RPC round trip
// to reject.
//
// The caller is usually a resource server, not a buyer, so size the limit for
// a server's whole traffic. Client IPs come from gin, which only honors
// X-Forwarded-For from TRUSTED_PROXIES.
func RateLimit(limiter ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limiter.Allow(c.ClientIP()) {
			c.Next()
			return
		}
		c.Header("Retry-After", "60")
		response.Error(c, http.StatusTooManyRequests, "too many requests from this client")
		c.Abort()
	}
}
