package payment

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/islu/x402-facilitator/internal/domain/payment"
	"github.com/islu/x402-facilitator/internal/domain/telemetry"
)

func Test_SettlePayment(t *testing.T) {
	tests := []struct {
		name            string
		amount          string
		setup           func(*serviceDeps)
		wantTransaction string
		wantErr         error
		wantReason      string
		wantMetrics     map[string]telemetry.Labels
	}{
		{
			name:   "a settled payment returns its transaction",
			amount: "1000",
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{
						Success:     true,
						Transaction: "0xdead",
						Network:     testNetwork,
						Payer:       testPayer,
					}, nil)
			},
			wantTransaction: "0xdead",
			wantMetrics: map[string]telemetry.Labels{
				metricSettlements: {"network": testNetwork, "scheme": testScheme, "success": "true"},
			},
		},
		{
			name:   "a failed settlement keeps the reason and any broadcast transaction",
			amount: "1000",
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{}, payment.NewSettlementError(
						"transaction_failed", "reverted", testPayer, testNetwork, "0xdead"))
			},
			wantReason:      "transaction_failed",
			wantTransaction: "0xdead",
			wantMetrics: map[string]telemetry.Labels{
				metricSettlements:    {"network": testNetwork, "scheme": testScheme, "success": "false"},
				metricSettleFailures: {"network": testNetwork, "reason": "transaction_failed"},
			},
		},
		{
			// The ceiling caps the blast radius of a compromised caller, so it
			// has to stop the payment before any gas is spent — no EXPECT on the
			// processor, which fails the test if it is reached.
			name:   "an amount above the ceiling never reaches the processor",
			amount: "2000000",
			setup: func(d *serviceDeps) {
				d.policy = ceilingPolicy(1_000_000)
			},
			wantReason: payment.ReasonAmountTooBig,
			wantMetrics: map[string]telemetry.Labels{
				metricRejected: {"network": testNetwork, "reason": payment.ReasonAmountTooBig},
			},
		},
		{
			name:   "an amount exactly at the ceiling settles",
			amount: "1000000",
			setup: func(d *serviceDeps) {
				d.policy = ceilingPolicy(1_000_000)
				d.processor.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{Success: true, Network: testNetwork}, nil)
			},
		},
		{
			name:   "an amount above the ceiling with no ceiling configured settles",
			amount: "999999999999",
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().
					Settle(gomock.Any(), gomock.Any()).
					Return(payment.SettleResult{Success: true, Network: testNetwork}, nil)
			},
		},
		{
			// A worthless token would otherwise have the settlement wallet pay
			// gas to move it.
			name:   "an asset outside the allowlist never reaches the processor",
			amount: "1000",
			setup: func(d *serviceDeps) {
				d.policy = payment.Policy{Assets: map[string]payment.AssetLimit{
					payment.AssetKey(testNetwork, "0x000000000000000000000000000000000000dead"): {},
				}}
			},
			wantReason: payment.ReasonAssetNotAllowed,
			wantMetrics: map[string]telemetry.Labels{
				metricRejected: {"network": testNetwork, "reason": payment.ReasonAssetNotAllowed},
			},
		},
		{
			name:   "a rate limited payer never reaches the processor",
			amount: "1000",
			setup: func(d *serviceDeps) {
				d.limiter = &countingLimiter{remaining: 0}
			},
			wantReason: payment.ReasonRateLimited,
			wantMetrics: map[string]telemetry.Labels{
				metricRateLimited: {"operation": "settle"},
			},
		},
		{
			name:   "a system error passes through without counting a settlement",
			amount: "1000",
			setup: func(d *serviceDeps) {
				d.processor.EXPECT().Settle(gomock.Any(), gomock.Any()).Return(payment.SettleResult{}, errRPCDown)
			},
			wantErr: errRPCDown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			metrics := newRecordingRecorder()
			deps.metrics = metrics
			tt.setup(&deps)

			result, err := newService(deps).SettlePayment(context.Background(), evmRequest(t, testPayer, tt.amount))

			switch {
			case tt.wantErr != nil:
				require.ErrorIs(t, err, tt.wantErr)
				var failed *payment.SettlementError
				assert.NotErrorAs(t, err, &failed, "a system error must not look like a settlement verdict")
				assert.Zero(t, metrics.count(metricSettlements, telemetry.Labels{
					"network": testNetwork, "scheme": testScheme, "success": "false",
				}))
			case tt.wantReason != "":
				var failed *payment.SettlementError
				require.ErrorAs(t, err, &failed)
				assert.Equal(t, tt.wantReason, failed.Reason)
				assert.Equal(t, tt.wantTransaction, failed.Transaction)
			default:
				require.NoError(t, err)
				assert.True(t, result.Success)
				assert.Equal(t, tt.wantTransaction, result.Transaction)
			}

			for name, labels := range tt.wantMetrics {
				assert.Equal(t, 1, metrics.count(name, labels), "metric %s%v", name, labels)
			}
		})
	}
}

func ceilingPolicy(max int64) payment.Policy {
	return payment.Policy{Assets: map[string]payment.AssetLimit{
		payment.AssetKey(testNetwork, testAsset): {Max: big.NewInt(max)},
	}}
}

func Test_SettlePayment_Idempotent(t *testing.T) {
	settled := payment.SettleResult{Success: true, Transaction: "0xdead", Network: testNetwork, Payer: testPayer}
	reverted := payment.NewSettlementError("transaction_failed", "reverted", testPayer, testNetwork, "0xdead")

	tests := []struct {
		name string
		// first is what the processor answers the first attempt with.
		firstResult payment.SettleResult
		firstErr    error
		// wantCalls is how many times a retry reaches the processor in total.
		wantCalls int
	}{
		{
			// The mechanism would answer a retry with "nonce already used",
			// telling the resource server a charged payer did not pay.
			name:        "a retry after success replays the recorded transaction",
			firstResult: settled,
			wantCalls:   1,
		},
		{
			name:      "a retry after a broadcast failure replays it instead of re-broadcasting",
			firstErr:  reverted,
			wantCalls: 1,
		},
		{
			// Nothing reached the chain, so there is nothing to protect.
			name:      "a retry after an outage tries again",
			firstErr:  errRPCDown,
			wantCalls: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			calls := 0
			deps.processor.EXPECT().
				Settle(gomock.Any(), gomock.Any()).
				DoAndReturn(func(context.Context, payment.Request) (payment.SettleResult, error) {
					calls++
					if calls == 1 {
						return tt.firstResult, tt.firstErr
					}
					return settled, nil
				}).
				AnyTimes()
			svc := newService(deps)
			req := evmRequest(t, testPayer, "1000")
			// Same document, different whitespace: a retry that re-serialized it.
			retry := payment.Request{Payload: append([]byte(" "), req.Payload...), Requirements: req.Requirements}

			first, firstErr := svc.SettlePayment(context.Background(), req)
			second, secondErr := svc.SettlePayment(context.Background(), retry)

			assert.Equal(t, tt.wantCalls, calls)
			if tt.wantCalls == 1 {
				assert.Equal(t, first, second)
				assert.Equal(t, firstErr, secondErr)
			}
		})
	}
}

// Two attempts racing each other would both pass verification and both
// broadcast; the second must wait for the first instead.
func Test_SettlePayment_ConcurrentDuplicateJoinsInFlight(t *testing.T) {
	ctrl := gomock.NewController(t)
	deps := newServiceDeps(ctrl)
	started, release := make(chan struct{}), make(chan struct{})
	deps.processor.EXPECT().
		Settle(gomock.Any(), gomock.Any()).
		DoAndReturn(func(context.Context, payment.Request) (payment.SettleResult, error) {
			close(started)
			<-release
			return payment.SettleResult{Success: true, Transaction: "0xdead"}, nil
		}).
		Times(1)
	svc := newService(deps)
	req := evmRequest(t, testPayer, "1000")

	leader := make(chan payment.SettleResult)
	go func() {
		result, _ := svc.SettlePayment(context.Background(), req)
		leader <- result
	}()
	<-started
	follower := make(chan payment.SettleResult)
	go func() {
		result, _ := svc.SettlePayment(context.Background(), req)
		follower <- result
	}()
	close(release)

	assert.Equal(t, "0xdead", (<-leader).Transaction)
	assert.Equal(t, "0xdead", (<-follower).Transaction)
}

func Test_ListSupported(t *testing.T) {
	tests := []struct {
		name       string
		supported  payment.Supported
		wantKinds  int
		wantReady  bool
		wantReason string
	}{
		{
			name: "registered kinds are passed through and the instance is ready",
			supported: payment.Supported{
				Kinds: []payment.Kind{{Version: 2, Scheme: testScheme, Network: testNetwork}},
			},
			wantKinds: 1,
			wantReady: true,
		},
		{
			// An instance that registered nothing accepts nothing, so it should
			// be drained rather than sent traffic.
			name:       "no kinds means not ready",
			supported:  payment.Supported{},
			wantKinds:  0,
			wantReady:  false,
			wantReason: "no payment kinds registered",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			deps.processor.EXPECT().Supported(gomock.Any()).Return(tt.supported).AnyTimes()
			svc := newService(deps)

			supported := svc.ListSupported(context.Background())
			ready, reason := svc.Ready(context.Background())

			assert.Len(t, supported.Kinds, tt.wantKinds)
			assert.Equal(t, tt.wantReady, ready)
			assert.Equal(t, tt.wantReason, reason)
		})
	}
}
