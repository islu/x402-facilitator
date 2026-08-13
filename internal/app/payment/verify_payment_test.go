package payment

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

var errRPCDown = errors.New("rpc endpoint unreachable")

func Test_VerifyPayment(t *testing.T) {
	tests := []struct {
		name string
		// requests are sent in order through the same service, so a case can
		// exercise state that builds up across calls (the rate limiter).
		requests    []payment.Request
		setup       func(*serviceDeps)
		wantValid   bool
		wantPayer   string
		wantErr     error
		wantReason  string
		wantMetrics map[string]telemetry.Labels
	}{
		{
			name:     "a valid payment is returned with its payer",
			requests: []payment.Request{evmRequest(t, testPayer, "1000")},
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().
					Verify(gomock.Any(), gomock.Any()).
					Return(payment.VerifyResult{IsValid: true, Payer: testPayer}, nil)
			},
			wantValid: true,
			wantPayer: testPayer,
			wantMetrics: map[string]telemetry.Labels{
				metricVerifications: {"network": testNetwork, "scheme": testScheme, "valid": "true"},
			},
		},
		{
			name:     "an invalid payment is reported as a verification error",
			requests: []payment.Request{evmRequest(t, testPayer, "1000")},
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().
					Verify(gomock.Any(), gomock.Any()).
					Return(payment.VerifyResult{}, payment.NewVerificationError("invalid_signature", "bad signature", testPayer))
			},
			wantReason: "invalid_signature",
			wantMetrics: map[string]telemetry.Labels{
				metricVerifications:  {"network": testNetwork, "scheme": testScheme, "valid": "false"},
				metricVerifyFailures: {"network": testNetwork, "reason": "invalid_signature"},
			},
		},
		{
			// An outage is not a verdict on the payment: counting it as invalid
			// would make a broken RPC endpoint look like a wave of bad payments.
			name:     "a system error passes through without counting the payment invalid",
			requests: []payment.Request{evmRequest(t, testPayer, "1000")},
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(payment.VerifyResult{}, errRPCDown)
			},
			wantErr: errRPCDown,
		},
		{
			name: "a rate limited payer never reaches the processor",
			requests: []payment.Request{
				evmRequest(t, testPayer, "1000"),
				evmRequest(t, testPayer, "1000"),
			},
			setup: func(d *serviceDeps) {
				d.limiter = &countingLimiter{remaining: 1}
				d.processor.EXPECT().
					Verify(gomock.Any(), gomock.Any()).
					Return(payment.VerifyResult{IsValid: true}, nil).
					Times(1)
			},
			wantReason: payment.ReasonRateLimited,
			wantMetrics: map[string]telemetry.Labels{
				metricRateLimited: {"operation": "verify"},
			},
		},
		{
			// Solana payloads carry an opaque transaction, so no payer is
			// readable without chain access. Those requests must not share a
			// quota with every other anonymous caller.
			name: "a payment with no readable payer is not rate limited",
			requests: []payment.Request{{
				Payload:      []byte(`{"x402Version":2,"payload":{"transaction":"AQAAA"}}`),
				Requirements: []byte(`{"scheme":"exact","network":"solana:EtWTRABZaYq6iMfeYKouRu166VU2xqa1","amount":"1000"}`),
			}},
			setup: func(d *serviceDeps) {
				d.limiter = &countingLimiter{remaining: 0}
				d.processor.EXPECT().Verify(gomock.Any(), gomock.Any()).Return(payment.VerifyResult{IsValid: true}, nil)
			},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			metrics := newRecordingRecorder()
			deps.metrics = metrics
			tt.setup(&deps)
			svc := newService(deps)

			var (
				result payment.VerifyResult
				err    error
			)
			for _, req := range tt.requests {
				result, err = svc.VerifyPayment(context.Background(), req)
			}

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
				var invalid *payment.VerificationError
				assert.NotErrorAs(t, err, &invalid, "a system error must not look like an invalid payment")
			case tt.wantReason != "":
				var invalid *payment.VerificationError
				require.ErrorAs(t, err, &invalid)
				assert.Equal(t, tt.wantReason, invalid.Reason)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantValid, result.IsValid)
				assert.Equal(t, tt.wantPayer, result.Payer)
			}

			for name, labels := range tt.wantMetrics {
				assert.Equal(t, 1, metrics.count(name, labels), "metric %s%v", name, labels)
			}
			assert.Contains(t, metrics.observed, metricVerifyDuration, "verification latency should be observed")
		})
	}
}

// The SDK detects the protocol version from these bytes, and unknown fields
// (extensions) must survive, so the documents cannot be re-encoded on the way in.
func Test_VerifyPayment_ForwardsDocumentsVerbatim(t *testing.T) {
	ctrl := gomock.NewController(t)
	deps := newServiceDeps(ctrl)
	req := evmRequest(t, testPayer, "1000")

	var seen payment.Request
	deps.processor.EXPECT().
		Verify(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, r payment.Request) (payment.VerifyResult, error) {
			seen = r
			return payment.VerifyResult{IsValid: true}, nil
		})

	_, err := newService(deps).VerifyPayment(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, req.Payload, seen.Payload)
	assert.Equal(t, req.Requirements, seen.Requirements)
}
