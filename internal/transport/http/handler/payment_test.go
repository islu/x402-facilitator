package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	apppayment "github.com/islu/x402-facilitator/internal/app/payment"
	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/ratelimit"
	infraratelimit "github.com/islu/x402-facilitator/internal/infra/ratelimit"
	"github.com/islu/x402-facilitator/internal/infra/telemetry"
	mocks "github.com/islu/x402-facilitator/internal/mocks/domain"
	"github.com/islu/x402-facilitator/internal/transport/http/middleware"
)

// test data
const (
	testNetwork = "eip155:84532"
	testPayer   = "0xabc"

	validBody = `{
  "x402Version": 2,
  "paymentPayload": {
    "x402Version": 2,
    "accepted": {"scheme": "exact", "network": "eip155:84532", "amount": "1000"},
    "payload": {"signature": "0xsig", "authorization": {"from": "0xabc"}}
  },
  "paymentRequirements": {"scheme": "exact", "network": "eip155:84532", "amount": "1000"}
}`
)

// errRPCDown carries a provider URL with an API key, the way go-ethereum's RPC
// errors do, so tests can check it never reaches a response body.
var errRPCDown = errors.New(`post "https://base-sepolia.example/v2/SECRET-API-KEY": dial tcp: connection refused`)

// testServer is the public and admin routers wired over the real payment
// service and a mocked processor, so the tests exercise the same handler → use
// case → port path production uses.
type testServer struct {
	public    http.Handler
	admin     http.Handler
	processor *mocks.MockProcessor
}

func newTestServer(ctrl *gomock.Controller, clients ratelimit.Limiter) testServer {
	processor := mocks.NewMockProcessor(ctrl)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := telemetry.New()

	service := apppayment.NewService(processor, disabledLimiter{}, registry, payment.Policy{}, logger)
	h := NewHandler(service, registry, Timeouts{Verify: time.Second, Settle: time.Second})

	gin.SetMode(gin.TestMode)
	public := gin.New()
	public.Use(middleware.Recovery(logger))
	RegisterPaymentRoutes(public, h, middleware.RateLimit(clients))
	RegisterSystemRoutes(public, h)

	admin := gin.New()
	RegisterAdminRoutes(admin, h)

	return testServer{public: public, admin: admin, processor: processor}
}

func newTestRouter(ctrl *gomock.Controller) (http.Handler, *mocks.MockProcessor) {
	server := newTestServer(ctrl, disabledLimiter{})
	return server.public, server.processor
}

type disabledLimiter struct{}

func (disabledLimiter) Allow(string) bool    { return true }
func (disabledLimiter) Exceeded(string) bool { return false }
func (disabledLimiter) Enabled() bool        { return false }

func post(t *testing.T, handler http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func get(t *testing.T, handler http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), "body: %s", rec.Body.String())
	return out
}

func Test_Verify(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		setup      func(*mocks.MockProcessor)
		wantStatus int
		// wantBody is compared field by field when the response carries a
		// protocol body.
		wantBody *VerifyResponse
	}{
		{
			name: "a valid payment answers 200 with the protocol body",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().
					Verify(gomock.Any(), gomock.Any()).
					Return(payment.VerifyResult{IsValid: true, Payer: testPayer}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody:   &VerifyResponse{IsValid: true, Payer: testPayer},
		},
		{
			// An invalid payment is a successful answer of "no": the resource
			// server needs the reason so it rejects the buyer, not retries.
			name: "an invalid payment answers 200 with isValid false",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().
					Verify(gomock.Any(), gomock.Any()).
					Return(payment.VerifyResult{}, payment.NewVerificationError("invalid_signature", "bad signature", testPayer))
			},
			wantStatus: http.StatusOK,
			wantBody: &VerifyResponse{
				IsValid:        false,
				InvalidReason:  "invalid_signature",
				InvalidMessage: "bad signature",
				Payer:          testPayer,
			},
		},
		{
			name: "a system error answers 500 so the caller retries",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(payment.VerifyResult{}, errRPCDown)
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "malformed JSON answers 400",
			body:       `{"paymentPayload":`,
			setup:      func(*mocks.MockProcessor) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "a missing payload answers 400",
			body:       `{"paymentRequirements": {"scheme": "exact"}}`,
			setup:      func(*mocks.MockProcessor) {},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing requirements answers 400",
			body:       `{"paymentPayload": {"scheme": "exact"}}`,
			setup:      func(*mocks.MockProcessor) {},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			router, processor := newTestRouter(ctrl)
			tt.setup(processor)

			rec := post(t, router, "/verify", tt.body)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody == nil {
				return
			}
			body := decodeBody[VerifyResponse](t, rec)
			assert.Equal(t, *tt.wantBody, body)
			// The SDK's facilitator client rejects a verify response that omits
			// isValid, so it must be present even when false.
			assert.Contains(t, decodeBody[map[string]any](t, rec), "isValid")
		})
	}
}

func Test_Settle(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		setup      func(*mocks.MockProcessor)
		wantStatus int
		wantBody   *SettleResponse
	}{
		{
			name: "a settled payment answers 200 with the transaction",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{
						Success:     true,
						Transaction: "0xdead",
						Network:     testNetwork,
						Payer:       testPayer,
					}, nil)
			},
			wantStatus: http.StatusOK,
			wantBody: &SettleResponse{
				Success:     true,
				Transaction: "0xdead",
				Network:     testNetwork,
				Payer:       testPayer,
			},
		},
		{
			// A failed settlement still carries the transaction and network, so
			// the resource server can look up a payment that was broadcast and
			// then failed.
			name: "a failed settlement answers 200 with the reason",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{}, payment.NewSettlementError(
						"transaction_failed", "reverted", testPayer, testNetwork, "0xdead"))
			},
			wantStatus: http.StatusOK,
			wantBody: &SettleResponse{
				Success:      false,
				ErrorReason:  "transaction_failed",
				ErrorMessage: "reverted",
				Payer:        testPayer,
				Transaction:  "0xdead",
				Network:      testNetwork,
			},
		},
		{
			name: "a system error answers 500",
			body: validBody,
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().Settle(gomock.Any(), gomock.Any()).Return(payment.SettleResult{}, errRPCDown)
			},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "a missing payload answers 400",
			body:       `{"paymentRequirements": {"scheme": "exact"}}`,
			setup:      func(*mocks.MockProcessor) {},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			router, processor := newTestRouter(ctrl)
			tt.setup(processor)

			rec := post(t, router, "/settle", tt.body)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody == nil {
				return
			}
			assert.Equal(t, *tt.wantBody, decodeBody[SettleResponse](t, rec))
			// The SDK's facilitator client requires all three on every settle
			// response, success or failure.
			body := decodeBody[map[string]any](t, rec)
			assert.Contains(t, body, "success")
			assert.Contains(t, body, "transaction")
			assert.Contains(t, body, "network")
		})
	}
}

// The SDK detects the protocol version from these bytes, and unknown fields
// (extensions) must survive, so the documents cannot be re-encoded.
func Test_Verify_ForwardsDocumentsVerbatim(t *testing.T) {
	ctrl := gomock.NewController(t)
	router, processor := newTestRouter(ctrl)

	var seen payment.Request
	processor.EXPECT().
		Verify(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, req payment.Request) (payment.VerifyResult, error) {
			seen = req
			return payment.VerifyResult{IsValid: true}, nil
		})

	post(t, router, "/verify", validBody)

	var forwarded map[string]any
	require.NoError(t, json.Unmarshal(seen.Payload, &forwarded))
	assert.Equal(t, float64(2), forwarded["x402Version"])
	assert.Contains(t, forwarded, "payload")
}

func Test_Supported(t *testing.T) {
	tests := []struct {
		name          string
		supported     payment.Supported
		wantKinds     []SupportedKind
		wantSigners   map[string][]string
		wantBodyPiece string
	}{
		{
			name: "registered kinds are advertised in protocol shape",
			supported: payment.Supported{
				Kinds:   []payment.Kind{{Version: 2, Scheme: "exact", Network: testNetwork}},
				Signers: map[string][]string{"eip155:*": {"0xfacilitator"}},
			},
			wantKinds:   []SupportedKind{{X402Version: 2, Scheme: "exact", Network: testNetwork}},
			wantSigners: map[string][]string{"eip155:*": {"0xfacilitator"}},
		},
		{
			// The protocol expects an array; a nil slice would serialize as null.
			name:          "extensions serialize as an array, never null",
			supported:     payment.Supported{},
			wantKinds:     []SupportedKind{},
			wantBodyPiece: `"extensions":[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			router, processor := newTestRouter(ctrl)
			processor.EXPECT().Supported(gomock.Any()).Return(tt.supported)

			rec := get(t, router, "/supported")

			require.Equal(t, http.StatusOK, rec.Code)
			body := decodeBody[SupportedResponse](t, rec)
			assert.Equal(t, tt.wantKinds, body.Kinds)
			assert.Equal(t, tt.wantSigners, body.Signers)
			if tt.wantBodyPiece != "" {
				assert.Contains(t, rec.Body.String(), tt.wantBodyPiece)
			}
		})
	}
}

func Test_SystemRoutes(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		setup      func(*mocks.MockProcessor)
		wantStatus int
	}{
		{
			// Liveness must not depend on a chain: a probe that fails on an
			// outage gets the container killed instead of drained.
			name:       "healthz touches no dependency",
			path:       "/healthz",
			setup:      func(*mocks.MockProcessor) {},
			wantStatus: http.StatusOK,
		},
		{
			name: "readyz drains an instance with no registered kinds",
			path: "/readyz",
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().Supported(gomock.Any()).Return(payment.Supported{})
			},
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name: "readyz passes once a kind is registered",
			path: "/readyz",
			setup: func(p *mocks.MockProcessor) {
				p.EXPECT().Supported(gomock.Any()).Return(payment.Supported{
					Kinds: []payment.Kind{{Version: 2, Scheme: "exact", Network: testNetwork}},
				})
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			router, processor := newTestRouter(ctrl)
			tt.setup(processor)

			assert.Equal(t, tt.wantStatus, get(t, router, tt.path).Code)
		})
	}
}

func Test_Metrics(t *testing.T) {
	ctrl := gomock.NewController(t)
	server := newTestServer(ctrl, disabledLimiter{})
	server.processor.EXPECT().
		Verify(gomock.Any(), gomock.Any()).
		Return(payment.VerifyResult{IsValid: true}, nil)

	post(t, server.public, "/verify", validBody)
	rec := get(t, server.admin, "/metrics")

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/plain")
	assert.Contains(t, rec.Body.String(), "facilitator_verifications_total")
	// Wallet balances and traffic are for operators, not every caller.
	assert.Equal(t, http.StatusNotFound, get(t, server.public, "/metrics").Code)
}

// RPC errors embed the provider URL, and with it the API key; a caller must
// only learn that it should retry.
func Test_SystemErrorHidesCause(t *testing.T) {
	for _, path := range []string{"/verify", "/settle"} {
		t.Run(path, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			router, processor := newTestRouter(ctrl)
			processor.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(payment.VerifyResult{}, errRPCDown).AnyTimes()
			processor.EXPECT().Settle(gomock.Any(), gomock.Any()).Return(payment.SettleResult{}, errRPCDown).AnyTimes()

			rec := post(t, router, path, validBody)

			require.Equal(t, http.StatusInternalServerError, rec.Code)
			assert.NotContains(t, rec.Body.String(), "SECRET-API-KEY")
		})
	}
}

func Test_OversizedBody(t *testing.T) {
	ctrl := gomock.NewController(t)
	router, _ := newTestRouter(ctrl)
	body := `{"paymentPayload": {"pad": "` + strings.Repeat("a", maxBodyBytes) + `"}}`

	rec := post(t, router, "/verify", body)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

// Unproven traffic — forged or junk payloads — is bounded per caller IP before
// it costs an RPC call; /supported is a cheap read and stays open.
func Test_ClientRateLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	server := newTestServer(ctrl, infraratelimit.New(1, time.Minute))
	server.processor.EXPECT().
		Verify(gomock.Any(), gomock.Any()).
		Return(payment.VerifyResult{IsValid: true}, nil).
		Times(1)
	server.processor.EXPECT().Supported(gomock.Any()).Return(payment.Supported{}).AnyTimes()

	assert.Equal(t, http.StatusOK, post(t, server.public, "/verify", validBody).Code)
	assert.Equal(t, http.StatusTooManyRequests, post(t, server.public, "/settle", validBody).Code,
		"verify and settle share the caller's quota")
	assert.Equal(t, http.StatusOK, get(t, server.public, "/supported").Code)
}
