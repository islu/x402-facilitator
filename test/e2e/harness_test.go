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

	"github.com/stretchr/testify/require"
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

func (f *facilitator) healthy() bool {
	resp, err := http.Get(f.url + "/healthz")
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type verifyResponse struct {
	IsValid       bool   `json:"isValid"`
	InvalidReason string `json:"invalidReason"`
	Payer         string `json:"payer"`
}

type settleResponse struct {
	Success     bool   `json:"success"`
	ErrorReason string `json:"errorReason"`
	Transaction string `json:"transaction"`
	Network     string `json:"network"`
	Payer       string `json:"payer"`
}

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

// settleConcurrently sends the same payment n times at once, as a resource
// server retrying on a timeout would.
func (f *facilitator) settleConcurrently(body []byte, n int) []settleResponse {
	results := make([]settleResponse, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i] = f.settle(body) })
	}
	wg.Wait()
	return results
}

// post requires a 200: an invalid payment is still a successful answer.
func (f *facilitator) post(path string, body []byte, out any) {
	resp, err := http.Post(f.url+path, "application/json", bytes.NewReader(body))
	require.NoError(f.t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(f.t, http.StatusOK, resp.StatusCode, path)
	require.NoError(f.t, json.NewDecoder(resp.Body).Decode(out))
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
