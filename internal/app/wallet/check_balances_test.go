package wallet

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"maps"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/islu/x402-facilitator/internal/domain/telemetry"
	"github.com/islu/x402-facilitator/internal/domain/wallet"
	mocks "github.com/islu/x402-facilitator/internal/mocks/domain"
)

// test data
const (
	testNetwork  = "eip155:84532"
	otherNetwork = "eip155:1"
	testAddress  = "0xfacilitator"
)

type serviceDeps struct {
	wallets wallet.Registry
	metrics *recordingRecorder
	cfg     Config
	logger  *slog.Logger
}

func newServiceDeps(*gomock.Controller) serviceDeps {
	return serviceDeps{
		wallets: wallet.Registry{},
		metrics: newRecordingRecorder(),
		cfg:     Config{},
		logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newService(deps serviceDeps) *Service {
	return &Service{
		wallets: deps.wallets,
		metrics: deps.metrics,
		cfg:     deps.cfg,
		logger:  deps.logger,
	}
}

func ethBalance(atomic int64) wallet.Balance {
	return wallet.Balance{
		Network:     testNetwork,
		Address:     testAddress,
		Atomic:      big.NewInt(atomic),
		UnitDivisor: 1e18,
	}
}

func Test_CheckBalances(t *testing.T) {
	tests := []struct {
		name         string
		setup        func(*gomock.Controller, *serviceDeps)
		wantBalances int
		wantGauges   map[string]float64
		wantErrors   map[string]int
	}{
		{
			name: "a healthy wallet is exported as a gauge in whole units",
			setup: func(ctrl *gomock.Controller, d *serviceDeps) {
				reader := mocks.NewMockBalanceReader(ctrl)
				reader.EXPECT().Balance(gomock.Any()).Return(ethBalance(2e18), nil)
				d.wallets[testNetwork] = reader
			},
			wantBalances: 1,
			wantGauges:   map[string]float64{testNetwork: 2},
		},
		{
			// The gauge must keep updating for a low wallet: that is the series
			// an operator alerts on.
			name: "a low wallet is still reported",
			setup: func(ctrl *gomock.Controller, d *serviceDeps) {
				d.cfg = Config{MinBalance: big.NewInt(1e17)}
				reader := mocks.NewMockBalanceReader(ctrl)
				reader.EXPECT().Balance(gomock.Any()).Return(ethBalance(0), nil)
				d.wallets[testNetwork] = reader
			},
			wantBalances: 1,
			wantGauges:   map[string]float64{testNetwork: 0},
		},
		{
			// One unreachable chain must not hide the others, or a facilitator
			// serving several networks loses all balance visibility at once.
			name: "a failed read is counted and the sweep continues",
			setup: func(ctrl *gomock.Controller, d *serviceDeps) {
				broken := mocks.NewMockBalanceReader(ctrl)
				broken.EXPECT().Balance(gomock.Any()).Return(wallet.Balance{}, errors.New("rpc unreachable"))
				working := mocks.NewMockBalanceReader(ctrl)
				working.EXPECT().Balance(gomock.Any()).Return(ethBalance(1e18), nil)

				d.wallets[otherNetwork] = broken
				d.wallets[testNetwork] = working
			},
			wantBalances: 1,
			wantGauges:   map[string]float64{testNetwork: 1},
			wantErrors:   map[string]int{otherNetwork: 1},
		},
		{
			name:         "no wallets means nothing to do",
			setup:        func(*gomock.Controller, *serviceDeps) {},
			wantBalances: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			tt.setup(ctrl, &deps)

			balances := newService(deps).CheckBalances(context.Background())

			require.Len(t, balances, tt.wantBalances)
			for network, want := range tt.wantGauges {
				assert.InDelta(t, want, deps.metrics.gauge(metricBalance, telemetry.Labels{"network": network}), 1e-9)
			}
			for network, want := range tt.wantErrors {
				assert.Equal(t, want, deps.metrics.counter(metricBalanceError, telemetry.Labels{"network": network}))
			}
		})
	}
}

func Test_Watch(t *testing.T) {
	tests := []struct {
		name        string
		interval    time.Duration
		wantChecked bool
	}{
		{
			name:        "a zero interval disables the watch",
			interval:    0,
			wantChecked: false,
		},
		{
			// An hour-long interval: only the check that runs before the first
			// tick can satisfy this, which is what a boot-time warning needs.
			name:        "the first check runs before the first tick",
			interval:    time.Hour,
			wantChecked: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			deps := newServiceDeps(ctrl)
			checked := make(chan struct{}, 1)
			reader := mocks.NewMockBalanceReader(ctrl)
			reader.EXPECT().
				Balance(gomock.Any()).
				DoAndReturn(func(context.Context) (wallet.Balance, error) {
					checked <- struct{}{}
					return ethBalance(1e18), nil
				}).
				AnyTimes()
			deps.wallets[testNetwork] = reader

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			returned := make(chan struct{})
			go func() {
				newService(deps).Watch(ctx, tt.interval)
				close(returned)
			}()

			if !tt.wantChecked {
				select {
				case <-returned:
				case <-time.After(2 * time.Second):
					t.Fatal("Watch did not return for a disabled interval")
				}
				assert.Empty(t, checked, "no balance should have been read")
				return
			}

			select {
			case <-checked:
			case <-time.After(2 * time.Second):
				t.Fatal("Watch did not check balances on start")
			}
		})
	}
}

// recordingRecorder captures what the use case reported.
type recordingRecorder struct {
	counters map[string]int
	gauges   map[string]float64
}

func newRecordingRecorder() *recordingRecorder {
	return &recordingRecorder{counters: map[string]int{}, gauges: map[string]float64{}}
}

func (r *recordingRecorder) Inc(name string, labels telemetry.Labels) {
	r.counters[key(name, labels)]++
}

func (r *recordingRecorder) SetGauge(name string, labels telemetry.Labels, value float64) {
	r.gauges[key(name, labels)] = value
}

func (r *recordingRecorder) Observe(string, telemetry.Labels, time.Duration) {}
func (r *recordingRecorder) Describe(string, string)                         {}

func (r *recordingRecorder) counter(name string, labels telemetry.Labels) int {
	return r.counters[key(name, labels)]
}

func (r *recordingRecorder) gauge(name string, labels telemetry.Labels) float64 {
	return r.gauges[key(name, labels)]
}

func key(name string, labels telemetry.Labels) string {
	var b strings.Builder
	b.WriteString(name)
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		b.WriteString("|" + k + "=" + labels[k])
	}
	return b.String()
}
