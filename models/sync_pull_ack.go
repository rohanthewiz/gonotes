package models

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/rohanthewiz/logger"
	"github.com/rohanthewiz/serr"
)

// ============================================================================
// Acknowledged pull delivery
//
// A pull used to mark its page delivered as it served it: PullChanges called
// MarkSyncChangesForPeer before writing the response, and a marked change is
// never served to that peer again. Anything that stopped the page between
// the mark and the spoke's own storage lost it for good: the response dropped
// in transit, the spoke killed while decoding it, or the answer going to a
// process that had just died. Phones die mid-sync routinely (swipe-away, low
// memory, tunnels). On an Android emulator, 4 of 7 force-stops mid-sync lost a
// 100-change page each, and later syncs fetched nothing, because the hub had
// no record that anything was missing.
//
// A spoke now opts in with delivery=ack, and the hub leaves the page
// unmarked. The spoke secures the page in its own storage, then names its
// last change on its next pull, and the hub marks up to that change, not up to
// what it served:
//
//	spoke                                        hub
//	  │ pull ?delivery=ack                          │
//	  │◀──────────────────── page 1 (c1…c100), held ┤  nothing marked
//	  │ store c1…c100 and the ack (c100, sum)       │
//	  │   in one transaction                        │
//	  │ pull ?delivery=ack&ack=c100&ack_sum=…       │
//	  │                                             ├─ the stream up to c100 hashes
//	  │                                             │  to sum: mark c1…c100
//	  │◀─────────────────── page 2 (c101…c200), held┤
//	  │ store …                                     │
//	  │ pull ?…&ack=c200&ack_sum=…                  ├─ mark c101…c200
//	  │◀──────────────────────────── empty page ────┤  the closing pull
//
// If the spoke dies anywhere before its store commits, its next pull still
// carries the previous ack. The page was never marked, so it is served again.
// If it dies after the commit, the next pull carries this page's ack and the
// page is marked then. Either way nothing is lost, and the spoke never has to
// tell the two cases apart.
//
// # Why the ack carries a sum
//
// "Up to that change" is a prefix of the peer's unsent stream, re-cut when the
// ack arrives. That prefix is exactly the page served only if nothing
// changed ahead of the acknowledged change in between. Normal writes cannot
// break that, because a new change row is stamped CURRENT_TIMESTAMP and sorts
// after everything already served. The exception is hub compaction
// (CompactHubChangeLog), which deletes rows and writes their replacement at an
// old timestamp. A category's replacement takes its group's FIRST timestamp,
// so a group that straddles the acknowledged change puts a row ahead of it.
// That row carries edits the spoke never received, and marking it would
// silently lose them.
//
// The sum is a hash of the page's change GUIDs in served order (PullPageSum).
// The hub marks the prefix only if the prefix hashes the same, so it marks
// exactly the page or nothing. Nothing is the safe failure: the page is
// served again, re-cut from the stream as it now stands, and acknowledged on
// the following pull. A repeated change is a no-op on any spoke that
// recognises it by GUID. The desktop spoke does that in ApplyIncomingSyncChange,
// and the mobile spoke keeps a ring of recently staged GUIDs for it.
//
// # Compatibility
//
// Without delivery=ack the hub marks on serve, as before, so an existing
// desktop or phone spoke sees no difference. A spoke that asks for ack
// delivery from a hub that predates it is marked on serve and gets responses
// without Delivery set. It only needs to stop treating its acks as
// meaningful, and it costs one extra empty pull per cycle at most.
//
// The scan that finds the acknowledged change covers the same number of rows
// as the page (limit). In an unchanged stream the change is within that many
// rows. One further back has more rows ahead of it than the page had, so the
// prefix could not have matched anyway.
// ============================================================================

// PullDeliveryAck is the delivery query parameter value that asks a pull to
// hold its page for an acknowledgement, and the SyncPullResponse.Delivery
// value of a page the hub held.
const PullDeliveryAck = "ack"

// PullAckOutcome says what an acknowledgement did.
type PullAckOutcome string

const (
	// PullAckMarked: the stream up to the acknowledged change was the page,
	// and it is now marked delivered.
	PullAckMarked PullAckOutcome = "marked"
	// PullAckNotPending: the acknowledged change is not among the peer's
	// first limit unsent changes. Usually it was marked already. The ack is
	// a spoke's standing record of its last stored page, so it is repeated
	// on every pull until a newer page replaces it. It may also be gone
	// (compacted away) or never have existed. Nothing is marked.
	PullAckNotPending PullAckOutcome = "not_pending"
	// PullAckMismatch: the change is pending, but the stream up to it no
	// longer hashes to the page the spoke stored. Something was written
	// ahead of it, or removed from it, since it was served. Nothing is
	// marked, and the head of the stream is served again.
	PullAckMismatch PullAckOutcome = "mismatch"
)

// PullPageSum is the acknowledgement sum of a page: the first 16 hex digits of
// the SHA-256 of its change GUIDs, each followed by a newline, in served order.
// Spokes compute it over the changes they received, the hub over the prefix it
// is about to mark.
//
// 64 bits is ample. A collision would have to occur between two specific
// sequences of random UUIDs, not across a population, and the price of a
// false match is the compaction loss described above, not a corruption the
// sum itself could cause. The newline terminator keeps ("ab","c") and
// ("a","bc") apart.
//
// The gonotes_mobile spoke mirrors this in wire.PageSum, and both test suites
// pin the same vector.
func PullPageSum(guids []string) string {
	h := sha256.New()
	for _, g := range guids {
		h.Write([]byte(g))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// AckPulledPage applies a spoke's acknowledgement of the page it last stored.
// ackGUID is that page's last change, ackSum its PullPageSum, and limit the
// page size the spoke pulls with. On PullAckMarked it returns how many changes
// it marked delivered to peerID.
//
// userGUID scopes the stream exactly as the pull that served the page scoped
// it. A different scope would cut a different prefix, and the sum would refuse
// it.
//
// A change that fails to mark is logged and skipped, as MarkSyncChangesForPeer
// does. It stays unsent, so it is served again, which every spoke tolerates
// (see the header). Only a failure to read the stream is returned as an error.
func AckPulledPage(peerID, userGUID, ackGUID, ackSum string, limit int) (PullAckOutcome, int, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, _, err := unsentStreamForPeer(peerID, userGUID, limit)
	if err != nil {
		return "", 0, serr.Wrap(err, "failed to read the unsent stream for an acknowledgement")
	}

	end := -1
	for i, r := range rows {
		if r.guid() == ackGUID {
			end = i
			break
		}
	}
	if end < 0 {
		return PullAckNotPending, 0, nil
	}

	page := rows[:end+1]
	guids := make([]string, len(page))
	for i, r := range page {
		guids[i] = r.guid()
	}
	if PullPageSum(guids) != ackSum {
		logger.Info("Sync pull acknowledgement does not match the stream; serving the head again",
			"peer_id", peerID,
			"ack", ackGUID,
			"prefix_len", len(page),
		)
		return PullAckMismatch, 0, nil
	}

	marked := 0
	for _, r := range page {
		var err error
		if r.note != nil {
			err = MarkChangeSyncedToPeer(r.note.ID, peerID)
		} else {
			err = MarkCategoryChangeSyncedToPeer(r.cat.ID, peerID)
		}
		if err != nil {
			logger.LogErr(err, "failed to mark an acknowledged change as synced",
				"change_guid", r.guid(), "peer_id", peerID)
			continue
		}
		marked++
	}
	return PullAckMarked, marked, nil
}
