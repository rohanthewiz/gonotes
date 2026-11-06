package models

import (
	"database/sql"
	"fmt"
	"slices"
	"testing"
	"time"
)

// TestPullStreamOrderIsTotalAtATie writes a run of changes that share one
// timestamp, as a bulk import or a compaction pass does, and walks them in
// pages smaller than the run, acknowledging each page.
//
// Two things must hold for acknowledgements to work at a tie. The order must
// be the same on every cut (categories first, then id), or an intact page
// re-cut for its ack would hash differently. And each database's LIMIT must
// keep the rows the merged order wants, which needs the id in the ORDER BY as
// well as in the Go sort. With created_at alone, SQL leaves the order inside a
// tie, and so the subset a LIMIT keeps there, to the engine.
//
// bytdb happens to return ties in id order today, so this test passes with or
// without the tie-break. It pins the property the acknowledgement relies on,
// so that an engine change that breaks it fails here.
func TestPullStreamOrderIsTotalAtATie(t *testing.T) {
	if err := InitTestDB(t.TempDir()); err != nil {
		t.Fatalf("init test db: %v", err)
	}
	t.Cleanup(func() { CloseDB() })

	at := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	for i := 1; i <= 5; i++ {
		if err := insertNoteChangeAt(pubDB, fmt.Sprintf("tie-n%d", i), fmt.Sprintf("tie-note-%d", i),
			OperationCreate, sql.NullInt64{}, "", at); err != nil {
			t.Fatalf("insert note change: %v", err)
		}
	}
	for i := 1; i <= 2; i++ {
		if err := insertCategoryChangeAt(fmt.Sprintf("tie-c%d", i), fmt.Sprintf("tie-cat-%d", i),
			OperationCreate, sql.NullInt64{}, "", at); err != nil {
			t.Fatalf("insert category change: %v", err)
		}
	}
	// Ids rise in insertion order within each kind, so this is the order the
	// stream must produce.
	want := []string{"tie-c1", "tie-c2", "tie-n1", "tie-n2", "tie-n3", "tie-n4", "tie-n5"}

	const peer = "spoke-tie"
	var got []string
	for round := 0; round < 10; round++ {
		rows, _, err := unsentStreamForPeer(peer, "", 3)
		if err != nil {
			t.Fatalf("stream: %v", err)
		}
		if len(rows) == 0 {
			break
		}
		guids := make([]string, len(rows))
		for i, r := range rows {
			guids[i] = r.guid()
		}
		got = append(got, guids...)

		outcome, marked, err := AckPulledPage(peer, "", guids[len(guids)-1], PullPageSum(guids), 3)
		if err != nil || outcome != PullAckMarked || marked != len(guids) {
			t.Fatalf("round %d: ack of %v = %s, %d, %v; want every row marked", round, guids, outcome, marked, err)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged stream = %v, want %v", got, want)
	}
}
