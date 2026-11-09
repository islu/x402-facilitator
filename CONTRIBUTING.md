# Contributing

Thanks for helping. This is payment infrastructure: correctness and safety come
before features, and every change that touches settlement needs a test.

## Before you start

- **Security issues** go through [SECURITY.md](SECURITY.md), never a public issue.
- **Bugs and small fixes**: open a pull request directly, or an issue if you
  want to discuss first.
- **New features, chains or schemes**: open an issue first, so the design can be
  agreed before you write it.

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

A pull request merges only when every item that applies is true.
[CI](.github/workflows) checks the mechanical ones on every pull request:
`task check`, the race detector, `task generate` and `task fmt` leaving no
diff, `task e2e`, `task vulncheck`, the Docker build, CodeQL, and a review of
new dependencies.
The author confirms the rest in the description.

### Every pull request

- [ ] `task check` passes in the devbox environment.
- [ ] `task generate` leaves no diff.
- [ ] New behavior has a test; a bug fix has the test that fails without it.
      Tests run without a network.
- [ ] No private keys, API-keyed RPC URLs or filled-in `.env` anywhere in the
      diff or history.
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
- [ ] `task e2e` passes, and the change's behavior on a chain is covered there
      (the suite runs on local anvil and Solana test validator chains).
- [ ] A change to how transactions are built or sent (gas, fees, nonces, RPC
      handling) also gets a manual testnet run (verify → settle → duplicate
      settle) with the transaction hashes in the description.
- [ ] Reviewed by a maintainer other than the author when one is available;
      a solo maintainer waits a day and re-reads the diff before merging.

Security fixes follow [SECURITY.md](SECURITY.md) and may merge privately ahead
of disclosure.

## Releases

A version is tagged when its planned scope is done and CI passes on `main`.
Pushing the tag publishes the image to
`ghcr.io/islu/x402-facilitator` with an SBOM and a build provenance
attestation. Each tag also gets a
[GitHub release](https://github.com/islu/x402-facilitator/releases) whose notes
open with the highlights and any upgrade steps — settings to change, behavior
that differs — above the generated list of changes.

**Versioning.** Before 1.0, a minor release may change configuration
incompatibly — always with a boot failure that names the replacement setting —
and a patch release only fixes bugs. From 1.0, the project follows semantic
versioning strictly: an incompatible change to configuration or the HTTP API
needs a major release.

## License

By contributing, you agree that your contributions are licensed under the
[MIT License](LICENSE).
