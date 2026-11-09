## Why

<!-- What problem does this solve? Link the issue if there is one. -->

## What changed

<!-- The change in a few lines. Call out anything breaking. -->

## Merge criteria

Tick what applies; delete sections that do not. Details:
[CONTRIBUTING.md](../blob/main/CONTRIBUTING.md#merge-criteria).

### Every pull request

- [ ] CI passes
- [ ] New behavior has a test; a bug fix has the test that fails without it
- [ ] No private keys, API-keyed RPC URLs or filled-in `.env`
- [ ] The README matches the change

### Configuration or dependency changes

- [ ] `.env.example` and the README table updated; removed or renamed settings fail the boot with their replacement named

### Money paths (sign, broadcast, settle, or record a settlement)

- [ ] What a retry of the same payment does:
- [ ] Which transaction hash the caller sees on failure:
- [ ] Protocol response shapes and metric names unchanged, or called out as breaking
- [ ] `task e2e` covers the change
- [ ] If transaction building or sending changed — testnet run (verify → settle → duplicate settle), transaction hashes:
