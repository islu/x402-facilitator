# Security Policy

This service holds settlement-wallet keys and moves funds, so security reports
get priority over everything else.

## Reporting a vulnerability

Report privately through GitHub:
**[Report a vulnerability](https://github.com/islu/x402-facilitator/security/advisories/new)**
(the repository's Security tab → *Report a vulnerability*).

Please do not open a public issue, pull request or discussion for a suspected
vulnerability. Include what you can of:

- the affected version or commit,
- the configuration involved (networks, schemes, relevant settings — never real
  private keys),
- steps to reproduce, and the impact you observed or expect.

The project has a single maintainer, so handling is best effort: expect an
acknowledgement within two weeks, and a reminder on the advisory is welcome if
you hear nothing by then. After triage we agree a fix and disclosure timeline
with you. We credit reporters in the advisory unless you ask us not to.

## Supported versions

Fixes land on `main` and in the next release. Until the project tags a stable
release, only the latest commit on `main` is supported.

## Scope

In scope — anything in this repository that could let someone:

- extract or misuse a settlement-wallet key, or make the facilitator sign
  something it should not;
- settle a payment twice, settle without a valid authorization, or report a
  payment as settled or failed when it was not;
- bypass the asset allowlist, amount limits or rate limits, or drain the
  settlement wallet's gas;
- read internal details (RPC URLs, provider API keys, metrics) from the public
  port.

Out of scope:

- vulnerabilities in the x402 SDK itself — report those to
  [x402-foundation/x402](https://github.com/x402-foundation/x402/security);
- issues that need a misconfiguration the README warns against (for example an
  empty `ALLOWED_ASSETS` in production, or exposing `ADMIN_PORT` publicly);
- denial of service by traffic volume alone;
- findings on testnets that have no mainnet impact.

## Safe harbor

Good-faith research that respects this policy, avoids privacy violations and
service disruption, and uses only your own wallets and testnet funds will not
be pursued by us.
