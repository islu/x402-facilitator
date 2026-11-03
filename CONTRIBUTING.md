# Contributing

Thanks for helping. This is payment infrastructure: correctness and safety come
before features, and every change that touches settlement needs a test.

## Before you start

- **Security issues** go through [SECURITY.md](SECURITY.md), never a public issue.
- **Bugs and small fixes**: open a pull request directly, or an issue if you
  want to discuss first.
- **New features, chains or schemes**: open an issue first, so the design can be
  agreed before you write it. The [release milestones](#releases) show what is
  already planned.

## Setup

```bash
devbox shell        # pinned Go, golangci-lint, go-task, govulncheck
cp .env.example .env
task                # list the available tasks
```

Any Go 1.21+ toolchain works without devbox; see the README's
[Quick start](README.md#quick-start).

## Making a change

The project rules in [CLAUDE.md](CLAUDE.md) apply to every contributor, human or
agent — layering, protocol semantics, and how to treat code that broadcasts
transactions. The [README](README.md#architecture) explains the architecture.

Run before opening a pull request:

```bash
task generate   # only if you changed a port or a wire provider
task check      # fmt, vet, lint, test
task vulncheck  # if you changed dependencies
```

Tests are table-driven, use testify, and run without a network. A fix comes
with the test that would have caught it.

## Pull requests

Keep each pull request to one change and explain the *why* in the description.
Use [conventional commit](https://www.conventionalcommits.org/) subjects:
`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`.

## Merge criteria

A pull request merges only when every item that applies is true. There is no CI
yet, so the author confirms each one in the description and the reviewer
re-runs `task check`.

### Every pull request

- [ ] `task check` passes in the devbox environment.
- [ ] `task generate` leaves no diff.
- [ ] New behavior has a test; a bug fix has the test that fails without it.
      Tests run without a network.
- [ ] No private keys, API-keyed RPC URLs or filled-in `.env` anywhere in the
      diff or history.
- [ ] User-facing changes have an entry under *Unreleased* in
      [CHANGELOG.md](CHANGELOG.md).
- [ ] The README matches the change in behavior and configuration.

### Configuration or dependency changes

- [ ] `.env.example` and the README table are updated; a removed or renamed
      setting fails the boot with a message naming its replacement.
- [ ] `task vulncheck` reports no reachable vulnerabilities.

### Money paths

Anything that can sign, broadcast, settle, or record a settlement
(`internal/infra/chain`, `internal/infra/x402`, `internal/app/payment`, the
ledger):

- [ ] The description states what a retry of the same payment does and which
      transaction hash the caller sees on failure.
- [ ] Protocol response shapes and metric names are unchanged, or the change is
      called out as breaking.
- [ ] Until the e2e suite exists: a manual run on a testnet (verify → settle →
      duplicate settle) with the transaction hashes in the description.
- [ ] Reviewed by a maintainer other than the author when one is available;
      a solo maintainer waits a day and re-reads the diff before merging.

Security fixes follow [SECURITY.md](SECURITY.md) and may merge privately ahead
of disclosure.

## Releases

A version is tagged only when every requirement of its milestone is met and the
*Unreleased* section of [CHANGELOG.md](CHANGELOG.md) has been moved under it.

**Versioning.** Before 1.0, a minor release may change configuration
incompatibly — always with a boot failure that names the replacement setting —
and a patch release only fixes bugs. From 1.0, the project follows semantic
versioning strictly: an incompatible change to configuration or the HTTP API
needs a major release.

### v0.1.0 — evaluation (testnets)

For developers and operators trying the facilitator. Not for real funds.

- Solana networks are checked against their RPC endpoint at startup, as EVM
  networks already are.
- A manual Base Sepolia run of verify → settle → duplicate settle → stuck
  transaction cancellation, recorded with transaction hashes.
- GitHub private vulnerability reporting is enabled.
- The README states that v0.x is for testnets and limited pilots.

### v0.2.0 — limited mainnet pilots

For mainnet operation with low exposure: a dedicated wallet, a small balance,
and a tight `ALLOWED_ASSETS`.

- A durable settlement ledger (Postgres), with a query and export interface on
  the admin port.
- Recovery of settlements that confirm after a timeout, aligned with the
  upstream `settlement_pending` semantics, including after a restart.
- Several RPC endpoints per network with failover, and a readiness check based
  on endpoint health.
- `ALLOWED_ASSETS` is required in production mode.
- An end-to-end suite runs the full flow against an EVM fork (`task e2e`).
- A minimal runbook: refilling gas, rotating keys, rolling back.
- The settlement path has had a security review, with findings fixed or
  recorded.

### v1.0.0 — production

- 30 consecutive days of production operation with zero duplicate settlements
  and zero unrecorded settlements, with success-rate and confirmation-latency
  data.
- Stuck transactions are sped up rather than cancelled, and the response
  reports the hash that actually landed.
- Per-network settings (receipt timeout, gas, fee cap) and a startup check of
  every allowed token.
- The conformance suite covers EVM and Solana and produces a report.
- Signing through a KMS, with AWS KMS supported.
- A screening (KYT / sanctions) port with at least one provider adapter.
- A deployment package (Helm chart, docker-compose with dashboards) and
  per-network SLA metrics.
- CI builds release images reproducibly and scans them for vulnerabilities.
- An external security review of the signing and settlement paths.
- The configuration and HTTP API documented in the README become a
  compatibility commitment.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
