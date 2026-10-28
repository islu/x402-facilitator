# x402-facilitator

A production-shaped [x402](https://github.com/x402-foundation/x402) payment facilitator in Go.

A facilitator sits between resource servers and the chain: it **verifies** payment
signatures, **settles** them on-chain, and reports the result. Clients never touch
a node, and resource servers never hold a settlement wallet.

```text
Client ──▶ Resource Server ──▶ Facilitator ──▶ Network
              │  POST /verify   │  check signature, simulate
              │  ◀── isValid    │
              │  POST /settle   │  broadcast + wait for receipt
              │  ◀── tx hash    │
```

Built on [`github.com/x402-foundation/x402/go/v2`](https://github.com/x402-foundation/x402/tree/main/go),
following the structure in the SDK's [FACILITATOR.md](https://github.com/x402-foundation/x402/blob/main/go/FACILITATOR.md).

## What's included

| Capability | Where |
| --- | --- |
| Payment use cases: verify, settle, list supported, with rate limit + value ceiling | [internal/app/payment/](internal/app/payment/) |
| Payment domain: documents, results, policy, the processor port | [internal/domain/payment/](internal/domain/payment/) |
| x402 SDK adapter: scheme registration, error translation | [internal/infra/x402/](internal/infra/x402/) |
| EVM signer: EIP-712 recovery, EIP-1559 fees, nonce serialization, receipt polling | [internal/infra/chain/evm/signer.go](internal/infra/chain/evm/signer.go) |
| Solana signer: co-sign, simulate, send, confirm | [internal/infra/chain/svm/signer.go](internal/infra/chain/svm/signer.go) |
| `/verify`, `/settle`, `/supported` + `/healthz`, `/readyz`; `/metrics` on a separate admin port | [internal/transport/http/](internal/transport/http/) |
| Asset allowlist with per-asset min/max, idempotent `/settle`, stuck-transaction cancellation | [policy.go](internal/domain/payment/policy.go), [settlement_log.go](internal/app/payment/settlement_log.go), [signer.go](internal/infra/chain/evm/signer.go) |
| Wallet balance monitoring | [internal/app/wallet/](internal/app/wallet/) |
| Env-driven config with fail-fast validation | [internal/config/config.go](internal/config/config.go) |

Schemes registered per configured network:

- **EVM** — `exact` (EIP-3009 and Permit2 paths) and `upto` (Permit2), plus `exact` v1 under the legacy network name
- **SVM** — `exact`, plus `exact` v1, sharing one settlement cache so a payment cannot be settled twice while the first attempt is still confirming

## Quick start

The dev tools are pinned with [devbox](https://www.jetify.com/devbox) (Go,
golangci-lint, go-task, govulncheck) — `devbox shell`, then run everything
through `task`. wire and mockgen are pinned in go.mod's `tool` block and run
with `go tool`. Any Go 1.21+ toolchain works too; it downloads the version
go.mod requires. The Docker image may build with a newer patch release than
devbox has packaged — `task vulncheck` scans against the image's version.

```bash
devbox shell            # or bring your own Go toolchain
cp .env.example .env
# set EVM_PRIVATE_KEY to a funded key; the default network is Base Sepolia
task run
```

```bash
curl localhost:4022/supported
```

```json
{
  "kinds": [
    { "x402Version": 2, "scheme": "exact", "network": "eip155:84532" },
    { "x402Version": 2, "scheme": "upto", "network": "eip155:84532" },
    { "x402Version": 1, "scheme": "exact", "network": "base-sepolia" }
  ],
  "extensions": [],
  "signers": { "eip155": ["0x…"] }
}
```

Point a resource server at it:

```go
import x402http "github.com/x402-foundation/x402/go/v2/http"

client := x402http.NewHTTPFacilitatorClient(&x402http.FacilitatorConfig{
    URL: "http://localhost:4022",
})
```

The startup log lists every registered network with its settlement address —
fund those addresses, or settlement fails with `insufficient_funds`.

## Configuration

Everything is environment-driven; a `.env` file is loaded if present. See
[.env.example](.env.example) for the full annotated list. The essentials:

| Variable | Default | Purpose |
| --- | --- | --- |
| `EVM_RPC_URLS` | — | `network=url` pairs; each network gets schemes registered |
| `EVM_PRIVATE_KEY` | — | Settlement wallet, overridable per network via `EVM_PRIVATE_KEYS` |
| `SVM_RPC_URLS` / `SVM_PRIVATE_KEY` | — | Same, for Solana (optional) |
| `PORT` | `4022` | Listen port |
| `ADMIN_PORT` | `9090` | `/metrics` only; expose to the scraper, not the internet |
| `ALLOWED_ASSETS` | empty (any) | `network/asset=max` or `=min:max`, atomic units. **Set it in production** — otherwise the wallet pays gas to settle any token |
| `RATE_LIMIT_PER_MINUTE` | `0` (off) | Accepted verify+settle calls per payer |
| `RATE_LIMIT_PER_IP_PER_MINUTE` | `0` (off) | All verify+settle calls per caller IP — the bound on forged or junk traffic |
| `MAX_GAS_PRICE_GWEI` | uncapped | Refuse to broadcast above this fee |
| `EIP6492_ALLOWED_FACTORIES` | empty (deny) | Smart-wallet factories allowed during settlement |
| `VERIFY_TIMEOUT` / `SETTLE_TIMEOUT` | `30s` / `90s` | Per-request budgets |

Networks are [CAIP-2](https://github.com/ChainAgnostic/CAIPs/blob/main/CAIPs/caip-2.md)
identifiers (`eip155:8453`, `solana:5eykt4UsFv8P8NJdTREpY1vzqKqZKvdp`). Configuration
is validated at startup and every RPC endpoint is contacted — a wrong URL, an
unfunded chain ID mismatch or a missing key stops the boot instead of surfacing
on the first payment.

## API

### `POST /verify`

```json
{
  "x402Version": 2,
  "paymentPayload": { "…": "…" },
  "paymentRequirements": { "…": "…" }
}
```

Valid payment → `200`:

```json
{ "isValid": true, "payer": "0xabc…" }
```

Invalid payment → also `200`, because the facilitator answered successfully:

```json
{ "isValid": false, "invalidReason": "invalid_signature" }
```

`4xx`/`5xx` mean the facilitator could not reach an answer at all (malformed
request, RPC outage). That distinction matters: a resource server should reject
the buyer on `isValid: false`, but retry on `5xx`.

### `POST /settle`

Same body. Success → `200` with the transaction hash; failure → `200` with
`success: false`, a reason, and the transaction hash if one was broadcast:

```json
{ "success": true, "transaction": "0x…", "network": "eip155:84532", "payer": "0xabc…" }
```

### `GET /supported`

Registered `(version, scheme, network)` triples plus the settlement addresses.

### Operational endpoints

- `GET /healthz` — process liveness
- `GET /readyz` — `503` when no network is usable, so a load balancer can drain the instance
- `GET /metrics` — Prometheus text format, on `ADMIN_PORT` only

## Architecture

The layout is clean architecture: dependencies point inward, and the layer a
change belongs to is the layer you edit.

```text
transport/http  →  app  →  domain  ←  infra
   HTTP shape      use cases  types & ports   chains, x402 SDK, metrics
```

- **`internal/domain`** — payment documents, results, policy, and the ports
  (`Processor`, `Limiter`, `Recorder`, `BalanceReader`). Imports nothing but the
  standard library: no gin, no x402 SDK, no go-ethereum.
- **`internal/app`** — one package per area, `service.go` plus a file per use
  case. This is where policy lives: the rate limit and the settlement ceiling
  run here, before the payment reaches a chain.
- **`internal/infra`** — the adapters. [`infra/x402`](internal/infra/x402/) owns
  the protocol SDK and translates its errors into domain errors;
  [`infra/chain`](internal/infra/chain/) owns the signers; `infra/telemetry` and
  `infra/ratelimit` implement their ports.
- **`internal/transport/http`** — request binding, status mapping, and nothing
  else. The x402 endpoints write the protocol's bodies verbatim; this service's
  own endpoints use the `response` envelope.

Dependencies are wired by [google/wire](https://github.com/google/wire):
provider sets in `internal/{app,infra}/provider.go`, injector in
[cmd/facilitator/wire.go](cmd/facilitator/wire.go), generated output in
`wire_gen.go`. Run `task wire` after changing a provider.

### Extending

| Task | Where |
| --- | --- |
| Add a network | an entry in `EVM_RPC_URLS` — no code |
| Add a payment scheme | register it in [`infra/x402.NewProcessor`](internal/infra/x402/facilitator.go) |
| Persist settlements | replace the in-memory [`settlementLog`](internal/app/payment/settlement_log.go), which already sees every settlement's key, hash and outcome |
| Add a policy (fraud rules, allowlists) | a check in the use case, or a method on [`payment.Policy`](internal/domain/payment/policy.go) |
| Swap metric storage or the rate limiter | a new adapter under `internal/infra` — the ports do not move |
| Add auth | middleware in [transport/http](internal/transport/http/router.go); the endpoints are unauthenticated by design, since a facilitator normally serves many resource servers |

The x402 SDK's lifecycle hooks (`OnBeforeVerify`, `OnAfterSettle`, …) are still
available inside [`infra/x402`](internal/infra/x402/facilitator.go) if you need
mechanism-internal events. Policy deliberately does not live there: in the use
case it is testable without a chain and visible in one place.

## Production notes

**Wallets.** One wallet per network (`EVM_PRIVATE_KEYS`), separate from the
seller's `payTo` wallet. Keys come from the environment — mount them from a
secret manager rather than baking them into an image. For an HSM or KMS, replace
the signing calls in
[internal/infra/chain/evm/signer.go](internal/infra/chain/evm/signer.go);
nothing else in the codebase touches the key.

**Gas.** `facilitator_wallet_balance` is exported per network and warned on below
`MIN_BALANCE_WEI` (EVM) or `MIN_BALANCE_LAMPORTS` (Solana). Alert on it: a
facilitator out of gas keeps verifying happily while every settlement fails.
`ALLOWED_ASSETS` is the other half: without it anyone can deploy a worthless
EIP-3009 token and have your wallet pay gas to move it.

**Stuck transactions.** A settlement still unmined after `RECEIPT_TIMEOUT` is
replaced at the same nonce by a zero-value self-transfer at a higher fee, so one
underpriced transaction cannot block every later one. The settle response
reports failure with the original hash and the replacement's.

**Idempotency.** A `/settle` retry for the same payload joins the attempt in
flight, or — once a transaction was broadcast — gets the recorded outcome for
10 minutes instead of a second transaction.

**Scaling.** Instances are stateless apart from three things, all in-process:
nonces are serialized per process, so **each replica needs its own settlement
wallet**; the rate limiters need a shared store for a cluster-wide limit; and
the settlement log only deduplicates retries that reach the same replica. See
[BACKLOG.md](BACKLOG.md).

**Timeouts.** Verification is a signature check; settlement waits on block
inclusion. `RECEIPT_TIMEOUT` must stay below `SETTLE_TIMEOUT`, which is enforced
at startup.

**Duplicate settlement.** On Solana the same transaction can be submitted twice
before the first confirms, and the RPC reports success both times. The shared
`SettlementCache` rejects the second attempt with `duplicate_settlement`.

**Metrics.** Served on `ADMIN_PORT`. `facilitator_verifications_total`, `facilitator_settlements_total`,
`facilitator_verify_failures_total`, `facilitator_settle_failures_total`,
`facilitator_rate_limited_total`, `facilitator_settle_rejected_total`,
`facilitator_wallet_balance`, and duration
histograms for verify and settle. Alert on settlement failure rate and confirmation
latency, not just error counts.

## Layout

```text
cmd/facilitator/            entrypoint, wire injector, graceful shutdown
internal/config/            environment parsing and validation
internal/domain/
  payment/                  documents, results, policy, processor port
  wallet/                   settlement wallet balances + reader port
  ratelimit/ telemetry/     policy and metrics ports
internal/app/
  payment/                  verify / settle / list supported use cases
  wallet/                   balance checks and the watch loop
internal/infra/
  x402/                     x402 SDK adapter: schemes, error translation
  chain/evm/ chain/svm/     signers and settlement wallets
  ratelimit/ telemetry/     sliding window, Prometheus registry
  logger/                   slog setup
internal/transport/http/
  router.go                 route mounting
  handler/                  protocol + system endpoints
  middleware/ response/     logging, recovery, response shapes
internal/mocks/domain/      generated port mocks (mockgen)
```

## Development

Tasks are defined in [Taskfile.yml](Taskfile.yml); `task` alone lists them.

```bash
task check         # fmt + vet + lint + test
task test:race     # tests under the race detector
task test:cover    # coverage summary
task generate      # wire + mockgen
task docker:build  # container image
```

Run them from a `devbox shell` to get the pinned golangci-lint. Note that devbox
sets `GOPATH=$PWD/.go`, so its first run downloads dependencies into the project
directory (git-ignored) rather than reusing the global module cache.

Tests are table-driven and use testify. They run without a network: the use
cases drive a mocked `Processor` port (`task mockgen` regenerates the mocks from
the domain interfaces), the handler tests exercise the real handler → use case →
port path, and the rest cover config validation, document parsing, the
settlement ceiling, error translation, EIP-712 verification and the metrics
registry.

Verifying against a real chain needs testnet funds; point `EVM_RPC_URLS` at Base
Sepolia and drive it with the
[x402 Go client](https://github.com/x402-foundation/x402/blob/main/go/CLIENT.md).

## License

MIT — see [LICENSE](LICENSE).
