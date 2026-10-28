# x402-facilitator

Architecture, configuration and API: README.md. Planned work and its intended
design: BACKLOG.md — read the item before starting one, and add to it when you
defer something.

## Layering

- `internal/domain` imports only the standard library. A new outside dependency
  gets a port in `domain` and an adapter in `infra`, wired in
  `internal/{app,infra}/provider.go`.
- Policy (limits, allowlists, quotas) lives in the `app` use cases, where it is
  testable against a mocked `Processor`.
- After changing a port or provider, run `task generate` (wire + mockgen; the
  output is committed), then `task check`.
- Run project commands through `task`, inside the devbox environment
  (`eval "$(devbox shellenv)"`); devbox.json holds packages only.

## Protocol

- An invalid payment is a successful answer: 200 with `isValid:false` /
  `success:false` and a reason. 5xx means no verdict, and its body is a generic
  retry message — the cause goes to the log through `c.Error`, because RPC
  errors embed the provider URL and its API key.
- x402 bodies go out verbatim, without the `response` envelope; payment
  documents travel as raw JSON end to end.
- Metric names are a contract with dashboards: add new series, keep existing
  names.

## Money paths

A change that can broadcast a transaction states, in code or comment, what a
retry of the same payment does and which transaction hash the caller sees on
failure. The on-chain nonce prevents a double charge; the facilitator's job is
to never pay gas twice and never report a charged payer as unpaid.

## Conventions

- Deliberate shortcuts carry a `ponytail:` comment naming the ceiling and the
  upgrade path.
- A removed or renamed setting fails the boot with a message naming its
  replacement (see `MAX_SETTLE_AMOUNT` in `internal/config/config.go`).
- The shipped Go version is the Dockerfile's `golang:` base image, which may
  be a patch ahead of devbox's Go. Scan with `task vulncheck`, which targets the
  image's version; a bare `govulncheck` reports stdlib issues the image lacks.
