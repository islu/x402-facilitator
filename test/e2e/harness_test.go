//go:build e2e

// Package e2e runs the facilitator binary against local chains — anvil for
// EVM, solana-test-validator for Solana — with real HTTP and real transactions.
//
// Run it with `task e2e`; anvil, solc and solana-test-validator come with
// devbox. Each test starts its own chain and facilitator and generates its own
// keys, so tests share no state and need no funds.
package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	x402sdk "github.com/x402-foundation/x402/go/v2"
	"github.com/x402-foundation/x402/go/v2/types"
)

var facilitatorBin string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	dir, err := os.MkdirTemp("", "x402-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()

	facilitatorBin = filepath.Join(dir, "facilitator")
	if out, err := exec.Command("go", "build", "-o", facilitatorBin, "../../cmd/facilitator").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build facilitator: %v\n%s", err, out)
		return 1
	}
	if err := compileToken(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// ── Facilitator ──────────────────────────────────────────────────────────────

type facilitator struct {
	t   *testing.T
	url string
}

// startFacilitator runs the binary with the given network settings
// (EVM_RPC_URLS, keys, ALLOWED_ASSETS, overrides) on top of test defaults.
func startFacilitator(t *testing.T, env ...string) *facilitator {
	t.Helper()
	port, adminPort := freePort(t), freePort(t)
	cmd := exec.Command(facilitatorBin)
	// An empty working directory keeps a developer's .env out of the test.
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"PORT=" + port,
		"ADMIN_PORT=" + adminPort,
		"LOG_JSON=false",
		"LOG_LEVEL=debug",
		"ENABLE_V1=false",
		"BALANCE_CHECK_INTERVAL=1h",
	}, env...)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-exited
		if t.Failed() {
			t.Logf("facilitator log:\n%s", logs.String())
		}
	})

	f := &facilitator{t: t, url: "http://127.0.0.1:" + port}
	deadline := time.After(15 * time.Second)
	for !f.healthy() {
		select {
		case err := <-exited:
			exited <- err // for the cleanup
			t.Fatalf("facilitator exited at startup: %v", err)
		case <-deadline:
			t.Fatal("facilitator did not come up")
		case <-time.After(50 * time.Millisecond):
		}
	}
	return f
}

// Every request is bounded, so a facilitator that accepts a connection and
// never answers fails the test instead of hanging it. A settlement may wait out
// a receipt timeout and then a stuck transaction's outcome.
var (
	client      = &http.Client{Timeout: 2 * time.Minute}
	probeClient = &http.Client{Timeout: time.Second}
)

func (f *facilitator) healthy() bool {
	resp, err := probeClient.Get(f.url + "/healthz")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// The facilitator's answers, decoded into the SDK's own wire types.
type (
	verifyResponse = x402sdk.VerifyResponse
	settleResponse = x402sdk.SettleResponse
)

func (f *facilitator) verify(body []byte) verifyResponse {
	var out verifyResponse
	f.post("/verify", body, &out)
	return out
}

func (f *facilitator) settle(body []byte) settleResponse {
	var out settleResponse
	f.post("/settle", body, &out)
	return out
}

type settleAttempt struct {
	answer settleResponse
	err    error
}

// settleAsync sends a settlement and returns a function that waits for its
// answer, so a test can act on the chain while the facilitator waits on it.
// The request runs off the test goroutine, so its failure is reported when the
// test collects the answer.
func (f *facilitator) settleAsync(body []byte) func() settleResponse {
	done := make(chan settleAttempt, 1)
	go func() {
		var a settleAttempt
		a.err = f.call("/settle", body, &a.answer)
		done <- a
	}()
	return func() settleResponse {
		f.t.Helper()
		a := <-done
		require.NoError(f.t, a.err)
		return a.answer
	}
}

// settleOnce sends the same payment n times at once, as a resource server
// retrying on a timeout would, and requires one settlement: every caller gets
// the same successful answer.
func (f *facilitator) settleOnce(body []byte, n int) settleResponse {
	f.t.Helper()
	attempts := make([]settleAttempt, n)
	var wg sync.WaitGroup
	for i := range attempts {
		wg.Go(func() { attempts[i].err = f.call("/settle", body, &attempts[i].answer) })
	}
	wg.Wait()

	for i := range attempts {
		require.NoError(f.t, attempts[i].err)
	}
	settled := attempts[0].answer
	require.True(f.t, settled.Success, settled.ErrorReason)
	require.NotEmpty(f.t, settled.Transaction)
	for i := 1; i < n; i++ {
		assert.Equal(f.t, settled, attempts[i].answer)
	}
	return settled
}

// refuses asserts that verify and settle both refuse body for reason, and that
// settle reports no transaction.
func (f *facilitator) refuses(t *testing.T, body []byte, reason string) {
	t.Helper()
	verified := f.verify(body)
	assert.False(t, verified.IsValid)
	assert.Equal(t, reason, verified.InvalidReason)

	settled := f.settle(body)
	assert.False(t, settled.Success)
	assert.Equal(t, reason, settled.ErrorReason)
	assert.Empty(t, settled.Transaction)
}

// post requires a 200: an invalid payment is still a successful answer.
func (f *facilitator) post(path string, body []byte, out any) {
	f.t.Helper()
	require.NoError(f.t, f.call(path, body, out))
}

// call posts body and decodes a 200 answer into out. It returns its failure
// rather than failing the test, so it is safe off the test goroutine.
func (f *facilitator) call(path string, body []byte, out any) error {
	resp, err := client.Post(f.url+path, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// paymentBody is what a resource server sends to /verify and /settle.
func paymentBody(t *testing.T, payload types.PaymentPayload, req types.PaymentRequirements) []byte {
	t.Helper()
	payload.Accepted = req
	body, err := json.Marshal(map[string]any{
		"x402Version":         2,
		"paymentPayload":      payload,
		"paymentRequirements": req,
	})
	require.NoError(t, err)
	return body
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// freePort asks the OS for a port that is free right now. Another process
// could take it before it is used; on a test machine that is rare enough.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
}

// waitFor polls cond until it holds, failing the test after timeout.
func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	require.Eventually(t, cond, timeout, 50*time.Millisecond, what)
}
