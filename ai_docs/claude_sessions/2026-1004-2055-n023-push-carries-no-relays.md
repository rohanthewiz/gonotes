# Session: A Spoke's Push Carries No Relays (N-023)

**Session ID:** `4d84aea5-2d3c-4540-88ce-aaff8d3930a6`
**Date:** 2026-10-04
**Branch:** master
**Base commit:** `8302c0c` — Close N-026: adopt only the current hub's identity

Same session as
[2026-1004-2045-n049-category-link-lock-gate](2026-1004-2045-n049-category-link-lock-gate.md)
and [2026-1004-2051-n026-current-hub-identity](2026-1004-2051-n026-current-hub-identity.md).

Between N-026 and this item, N-025 came in again from the cats-todo backlog.
It was already closed in `2802d2e` (`gonotes account list|rename|merge`), so
nothing was changed, and the backlog entry was flagged to the user as stale.

Picks up N-023 from `ai_docs/todo/next-list.md`, first raised in
[2026-0818-1824-sync-prompt-mode-and-compaction](2026-0818-1824-sync-prompt-mode-and-compaction.md):

> `GetUnsentChangesForPeer` returns operation 9 (relay) rows. First written
> down as a missing operation filter. `2026-0818-1859` reframed it: with
> relays that behaviour is correct, but a spoke's push batch can carry relays
> the hub then skips by GUID, wasting one entry per batch. Filter per
> direction only if it shows up in a profile.

---

## Outcome: the premise was stale, so no code change

The item is conditional on a measurement, so the work was to measure. It came
out at zero: a spoke's push batch carries no relays in steady state.

### Why

Applying any pulled change records an `OperationSync` row under the change's
own GUID (`relayChangeGUID`). Right after that, the spoke's pull path marks it
as delivered to the hub:

```go
// models/sync_client.go, applyChangeWithConflictDetection
if err := ApplyIncomingSyncChange(change); err != nil { return err }
MarkChangeGUIDSyncedToPeer(change.GUID, sc.peerID)   // ← hub already has it
```

`pushChanges` builds its batch with `GetUnifiedChangesForPeer(sc.peerID, "", 100)`,
and that excludes everything in `note_change_sync_peers` /
`category_change_sync_peers` for that peer. So the relay rows never qualify.

The marking arrived in `cff9559` ("Let a change keep its name, so
hub-and-spokes converges"). That is the same relay work whose session doc
(`2026-0818-1859`) wrote the follow-up. Its own body says "a spoke marks a
pulled one as owed to nobody", but the follow-up didn't account for it.

The opposite direction is unchanged and must stay that way. On the hub,
`GetUnsentChangesForPeer` has to return relay rows, because they are what
other spokes pull. `TestAnAppliedChangeKeepsItsIdentity` already covers that.

### The only way a relay reaches a push

`MarkChangeGUIDSyncedToPeer` is best-effort. It logs a failed mark, and a
crash can land between apply and mark. In either case the relay is pushed
once. The hub finds its GUID (`changeGUIDExists`) and skips it, the skip
counts as accepted, and the spoke then marks it after the push
(`MarkSyncChangesForPeer`). That is one entry, once, and it clears itself. A
per-direction filter would save nothing measurable, so none was added.

## The measurement, kept as a test

`models/sync_push_relay_test.go`: `TestASpokesPushCarriesNoRelaysItPulled`
(package `models`, because `applyChangeWithConflictDetection` is unexported).

1. Builds `&SyncClient{config: &SyncConfig{}, peerID: spokePeer}`, with no
   HTTP.
2. Pulls a mixed batch through `applyChangeWithConflictDetection`:
   - a relayed note create and update
   - a relayed create followed by a privacy flip, whose relay row lands in the
     other database
   - a create and a delete
   - a relayed category create and rename
3. Guard: `GetUnifiedChangesForPeer("some-other-peer")` contains relay rows.
   This proves they exist and the test isn't passing on nothing.
4. Writes one local note.
5. Builds the push batch the way `pushChanges` does, and asserts:
   - no `OperationSync` entry
   - no pulled GUID
   - the local edit is present

**Negative check:** with the `MarkChangeGUIDSyncedToPeer(change.GUID,
sc.peerID)` line removed, the test fails on all seven relays (c1–c5, c7, c8).
The file was restored afterwards.

The `models` tests pass, and `gofmt` and `go vet` are clean.

## Files

- `models/sync_push_relay_test.go`: new test
- `ai_docs/todo/next-list.md`: N-023 moved to Closed

## Next

Closed: N-023. Declined: None. Raised: None.
Deferred: None. Promoted: None.
Updated: None. Full list: `ai_docs/todo/next-list.md`.
