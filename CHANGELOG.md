# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/) as described in
[CONTRIBUTING.md](CONTRIBUTING.md#releases).

## [Unreleased]

### Added

- x402 facilitator with `/verify`, `/settle` and `/supported`, built on the
  x402 Go SDK: EVM `exact` (EIP-3009 and Permit2) and `upto`, Solana `exact`,
  and the v1 protocol under legacy network names.
- Asset allowlist with per-asset minimum and maximum amounts
  (`ALLOWED_ASSETS`), enforced at verify and settle.
- Per-caller-IP rate limit (`RATE_LIMIT_PER_IP_PER_MINUTE`) and a per-payer
  quota that counts only payments the mechanism accepted.
- Idempotent `/settle`: a retry joins the attempt in flight, and a retry after
  broadcast gets the recorded outcome (in-process, per replica).
- Stuck EVM transactions are replaced at the same nonce when no receipt
  arrives, so they stop blocking later settlements.
- Startup validation of configuration and of every RPC endpoint's chain ID.
- Settlement-wallet balance monitoring with per-chain thresholds
  (`MIN_BALANCE_WEI`, `MIN_BALANCE_LAMPORTS`).
- Prometheus metrics on a separate admin port (`ADMIN_PORT`), plus `/healthz`
  and `/readyz`.
- `task vulncheck`, scanning against the Go version the Docker image ships.

### Security

- Server errors return a generic message; the cause, which can contain an RPC
  provider's API key, is only logged.
- Request bodies are capped at 64 KiB.
- Dependencies upgraded to clear reachable vulnerabilities in `golang.org/x/net`,
  `quic-go` and `gorilla/websocket`.
