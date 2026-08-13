package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/islu/x402-facilitator/internal/transport/http/response"
)

// RegisterSystemRoutes mounts the operational endpoints: liveness, readiness
// and the metrics scrape.
func RegisterSystemRoutes(router gin.IRoutes, h *Handler) {
	router.GET("/healthz", h.Healthz)
	router.GET("/readyz", h.Readyz)
	router.GET("/metrics", h.Metrics)
}

// Healthz reports that the process is up. It deliberately touches nothing else:
// a liveness probe that fails on a dependency outage gets the container killed
// instead of drained.
func (h *Handler) Healthz(c *gin.Context) {
	response.OK(c, gin.H{"status": "ok"})
}

// Readyz reports whether the facilitator can serve payments, so a load balancer
// can drain an instance that registered no usable network.
func (h *Handler) Readyz(c *gin.Context) {
	ready, reason := h.payment.Ready(c.Request.Context())
	if !ready {
		response.Error(c, http.StatusServiceUnavailable, reason)
		return
	}
	response.OK(c, gin.H{"status": "ok"})
}

// Metrics renders the collected metrics in Prometheus text format.
func (h *Handler) Metrics(c *gin.Context) {
	c.Header("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	c.Status(http.StatusOK)
	if err := h.exporter.Write(c.Writer); err != nil {
		// Headers are already committed, so the scrape gets a truncated body;
		// the log line is what tells an operator why.
		_ = c.Error(err)
	}
}
