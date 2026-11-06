package models_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"gonotes/models"
)

// Acknowledged pull delivery (sync_pull_ack.go). Each test plays a spoke:
// GetUnifiedChangesForPeer is the serve, which no longer marks anything, and
// AckPulledPage is what the spoke's next pull does with the page it stored.

// ackPeer is the spoke identity these tests pull as.
const ackPeer = "spoke-ack"

// pullPage serves the next page for ackPeer, scoped to the test user as the
// hub's handler scopes it.
func pullPage(t *testing.T, limit int) []models.SyncChange {
	t.Helper()
	resp, err := models.GetUnifiedChangesForPeer(ackPeer, spTestUserGUID, limit)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	return resp.Changes
}

func changeGUIDs(page []models.SyncChange) []string {
	out := make([]string, len(page))
	for i, c := range page {
		out[i] = c.GUID
	}
	return out
}

// ackFor is the acknowledgement a spoke sends for a page it stored: the last
// change's GUID and the sum over the whole page.
func ackFor(page []models.SyncChange) (string, string) {
	return page[len(page)-1].GUID, models.PullPageSum(changeGUIDs(page))
}

// TestPullPageSumVector pins the sum's definition. The mobile spoke's
// wire.PageSum test pins the same vector, so a change to either side fails
// a test rather than silently turning every acknowledgement into a mismatch.
func TestPullPageSumVector(t *testing.T) {
	if got := models.PullPageSum([]string{"c-1", "c-2"}); got != "e96ed84673bc175b" {
		t.Errorf("PullPageSum = %s, want e96ed84673bc175b", got)
	}
	// The per-GUID terminator keeps a shifted boundary from colliding.
	if models.PullPageSum([]string{"ab", "c"}) == models.PullPageSum([]string{"a", "bc"}) {
		t.Error("PullPageSum ignores where one GUID ends and the next begins")
	}
}

// TestAckPulledPageMarksExactlyThePage is the protocol's main path. A served
// page stays pending until it is acknowledged, the acknowledgement marks that
// page and nothing past it, and repeating it changes nothing.
func TestAckPulledPageMarksExactlyThePage(t *testing.T) {
	setupRelayTestDB(t)
	for i := 1; i <= 5; i++ {
		createTestNote(t, fmt.Sprintf("ack-note-%d", i), fmt.Sprintf("Ack %d", i))
	}
	all := changeGUIDs(pullPage(t, 100))
	if len(all) != 5 {
		t.Fatalf("expected 5 changes on the hub, got %d", len(all))
	}

	page1 := pullPage(t, 2)
	// Serving marks nothing: a spoke that died with this page in flight gets
	// it again on its next pull.
	if again := pullPage(t, 2); !slices.Equal(changeGUIDs(again), changeGUIDs(page1)) {
		t.Fatalf("an unacknowledged page was not served again: %v then %v",
			changeGUIDs(page1), changeGUIDs(again))
	}

	g, s := ackFor(page1)
	outcome, marked, err := models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 2)
	if err != nil || outcome != models.PullAckMarked || marked != 2 {
		t.Fatalf("ack = %s, %d, %v; want marked, 2", outcome, marked, err)
	}
	page2 := pullPage(t, 2)
	if !slices.Equal(changeGUIDs(page2), all[2:4]) {
		t.Fatalf("after the ack the next page = %v, want %v", changeGUIDs(page2), all[2:4])
	}

	// The spoke repeats its last ack until it stores a newer page (a lost
	// response, or the first pull of the next cycle). That marks nothing new.
	outcome, marked, err = models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 2)
	if err != nil || outcome != models.PullAckNotPending || marked != 0 {
		t.Fatalf("repeated ack = %s, %d, %v; want not_pending, 0", outcome, marked, err)
	}
	if again := pullPage(t, 2); !slices.Equal(changeGUIDs(again), all[2:4]) {
		t.Fatalf("a repeated ack moved the stream: %v", changeGUIDs(again))
	}

	// Page 2's ack; then the closing pull's page is the last change.
	g, s = ackFor(page2)
	if outcome, marked, _ = models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 2); marked != 2 {
		t.Fatalf("page 2 ack = %s, %d; want marked, 2", outcome, marked)
	}
	if rest := pullPage(t, 2); !slices.Equal(changeGUIDs(rest), all[4:]) {
		t.Fatalf("last page = %v, want %v", changeGUIDs(rest), all[4:])
	}
}

// TestAckPulledPageRefusesAWrongSum checks that an acknowledgement whose sum
// does not describe the stream up to its change marks nothing, even though
// the change it names is pending.
func TestAckPulledPageRefusesAWrongSum(t *testing.T) {
	setupRelayTestDB(t)
	for i := 1; i <= 3; i++ {
		createTestNote(t, fmt.Sprintf("sum-note-%d", i), fmt.Sprintf("Sum %d", i))
	}
	page := pullPage(t, 2)
	g, _ := ackFor(page)
	// A sum over the last change alone: the spoke claims a page that does not
	// start where the hub's stream does.
	outcome, marked, err := models.AckPulledPage(ackPeer, spTestUserGUID, g, models.PullPageSum([]string{g}), 2)
	if err != nil || outcome != models.PullAckMismatch || marked != 0 {
		t.Fatalf("ack = %s, %d, %v; want mismatch, 0", outcome, marked, err)
	}
	if again := pullPage(t, 2); !slices.Equal(changeGUIDs(again), changeGUIDs(page)) {
		t.Fatalf("a refused ack moved the stream: %v", changeGUIDs(again))
	}
}

// TestAckPulledPageRefusesAPageCompactionReordered is the case the sum exists
// for. Hub compaction between the serve and the ack writes a category's
// replacement at the category's FIRST timestamp, ahead of the acknowledged
// change, and that replacement carries a rename the spoke never received.
// Marking "everything up to the acknowledged change" would mark it and lose
// the rename. The sum sees the prefix is no longer the page, refuses, and the
// head is served again with the rename in it.
//
//	served:    c1(create "Straddle")  n1  n2 │ n3  c2(rename)
//	compacted: R(snapshot "Renamed") n1  n2 │ n3          R sits at c1's time
func TestAckPulledPageRefusesAPageCompactionReordered(t *testing.T) {
	setupRelayTestDB(t)

	cat := createTestCategory(t, "Straddle")
	time.Sleep(5 * time.Millisecond)
	for i := 1; i <= 3; i++ {
		createTestNote(t, fmt.Sprintf("straddle-note-%d", i), fmt.Sprintf("Straddle %d", i))
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := models.UpdateCategory(cat.ID, models.CategoryInput{Name: "Renamed"}, spTestUserGUID); err != nil {
		t.Fatalf("rename category: %v", err)
	}

	page := pullPage(t, 3)
	if len(page) != 3 || page[0].EntityType != "category" || page[2].EntityType != "note" {
		t.Fatalf("page = %+v, want the category create then two notes", page)
	}

	res, err := models.CompactHubChangeLog(0)
	if err != nil || res.CategoriesCompacted != 1 {
		t.Fatalf("hub compaction = %+v, %v; want the category compacted", res, err)
	}

	g, s := ackFor(page)
	outcome, marked, err := models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 3)
	if err != nil || outcome != models.PullAckMismatch || marked != 0 {
		t.Fatalf("ack after compaction = %s, %d, %v; want mismatch, 0", outcome, marked, err)
	}

	head := pullPage(t, 3)
	frag, ok := head[0].Fragment.(*models.CategoryFragmentOutput)
	if head[0].EntityType != "category" || !ok || frag.Name == nil || *frag.Name != "Renamed" {
		t.Fatalf("re-served head starts with %+v, want the compacted category carrying the rename", head[0])
	}
	if head[1].GUID != page[1].GUID || head[2].GUID != page[2].GUID {
		t.Fatalf("re-served head = %v, want the snapshot then the page's two notes", changeGUIDs(head))
	}

	// Acknowledging the re-served head works, and only the last note is left.
	g, s = ackFor(head)
	if outcome, marked, _ = models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 3); outcome != models.PullAckMarked || marked != 3 {
		t.Fatalf("ack of the re-served head = %s, %d; want marked, 3", outcome, marked)
	}
	if rest := pullPage(t, 3); len(rest) != 1 || rest[0].EntityGUID != "straddle-note-3" {
		t.Fatalf("after both acks the stream = %+v, want the third note alone", rest)
	}
}

// TestAckPulledPageIsScopedToThePeer checks that one spoke's acknowledgement
// leaves another spoke's stream alone. Delivery is tracked per peer, and an
// ack names a change, not a peer, so the scoping is the hub's job.
func TestAckPulledPageIsScopedToThePeer(t *testing.T) {
	setupRelayTestDB(t)
	for i := 1; i <= 2; i++ {
		createTestNote(t, fmt.Sprintf("scope-note-%d", i), fmt.Sprintf("Scope %d", i))
	}
	page := pullPage(t, 2)
	g, s := ackFor(page)
	if _, marked, _ := models.AckPulledPage(ackPeer, spTestUserGUID, g, s, 2); marked != 2 {
		t.Fatalf("marked %d, want 2", marked)
	}
	other, err := models.GetUnifiedChangesForPeer("spoke-other", spTestUserGUID, 2)
	if err != nil || len(other.Changes) != 2 {
		t.Fatalf("the other spoke's stream = %d changes (%v), want both still pending", len(other.Changes), err)
	}
}
