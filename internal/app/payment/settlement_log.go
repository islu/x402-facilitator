package payment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/islu/x402-facilitator/internal/domain/payment"
)

// settlementRetention is how long a broadcast settlement's outcome is replayed
// to retries. It only has to outlast a resource server's retry loop.
const settlementRetention = 10 * time.Minute

// settlementLog makes /settle idempotent per payment.
//
// Without it a retry is expensive or wrong either way: one that races the
// first attempt broadcasts a second transaction that reverts (gas spent for
// nothing), and one that arrives after success fails with the mechanism's
// "nonce already used" — telling the resource server a charged payer did not
// pay.
//
// ponytail: in-process memory. Retries routed to another replica, or arriving
// after a restart, are not deduplicated; the on-chain nonce still prevents a
// double charge. Back this with a durable shared store (the planned settlement
// ledger) when running more than one replica.
type settlementLog struct {
	mu        sync.Mutex
	entries   map[string]*settlementEntry
	lastPrune time.Time
}

// settlementEntry is one payment's settlement: in flight until done closes,
// then its outcome.
type settlementEntry struct {
	done     chan struct{}
	result   payment.SettleResult
	err      error
	finished time.Time
}

func newSettlementLog() *settlementLog {
	return &settlementLog{entries: map[string]*settlementEntry{}}
}

// settlementKey identifies a settlement by its signed payload and the
// requirements it is settled against: a recorded outcome is replayed only to
// the same request, never to one naming another payee or amount, which the
// mechanism would have refused. Whitespace is compacted so a retry that
// re-serializes the same documents still matches.
func settlementKey(req payment.Request) string {
	h := sha256.New()
	for _, doc := range [][]byte{req.Payload, req.Requirements} {
		var compact bytes.Buffer
		if err := json.Compact(&compact, doc); err != nil {
			compact.Reset()
			compact.Write(doc)
		}
		h.Write(compact.Bytes())
		h.Write([]byte{0}) // separator: no document contains a NUL byte
	}
	return hex.EncodeToString(h.Sum(nil))
}

// begin returns the entry for key and whether the caller is the one that must
// run the settlement. Any other caller waits on the returned entry.
func (l *settlementLog) begin(key string) (*settlementEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.lastPrune) >= time.Minute {
		l.pruneLocked(now)
	}
	if entry, ok := l.entries[key]; ok {
		return entry, false
	}
	entry := &settlementEntry{done: make(chan struct{})}
	l.entries[key] = entry
	return entry, true
}

// finish records the outcome and releases waiters.
//
// Only outcomes that must not be repeated are kept: a success, or a failure
// after a transaction was broadcast. Anything else — a policy refusal, an RPC
// outage before broadcast — is forgotten, so a retry gets a fresh attempt.
func (l *settlementLog) finish(key string, entry *settlementEntry, result payment.SettleResult, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry.result, entry.err, entry.finished = result, err, time.Now()
	close(entry.done)

	var failed *payment.SettlementError
	broadcast := errors.As(err, &failed) && failed.Transaction != ""
	if err != nil && !broadcast {
		delete(l.entries, key)
	}
}

// wait blocks until the entry's settlement finishes or ctx ends.
func (e *settlementEntry) wait(ctx context.Context) (payment.SettleResult, error) {
	select {
	case <-e.done:
		return e.result, e.err
	case <-ctx.Done():
		return payment.SettleResult{}, ctx.Err()
	}
}

// pruneLocked drops finished entries past retention. Callers must hold l.mu.
func (l *settlementLog) pruneLocked(now time.Time) {
	l.lastPrune = now
	for key, entry := range l.entries {
		select {
		case <-entry.done:
			if now.Sub(entry.finished) > settlementRetention {
				delete(l.entries, key)
			}
		default: // still in flight
		}
	}
}
