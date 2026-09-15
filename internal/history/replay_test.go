package history

import (
	"testing"

	"github.com/local/gopad/internal/crdt"
)

func TestBuildAtMatchesRecordedCheckpoints(t *testing.T) {
	a := crdt.CharID{SiteID: "history", Counter: 1}
	b := crdt.CharID{SiteID: "history", Counter: 2}
	c := crdt.CharID{SiteID: "history", Counter: 3}
	operations := []crdt.Operation{
		{Type: crdt.Insert, ID: a, Value: 'A'},
		{Type: crdt.Insert, ID: b, Value: 'B', LeftID: &a},
		{Type: crdt.Delete, ID: a},
		{Type: crdt.Insert, ID: c, Value: 'C', LeftID: &b},
		{Type: crdt.Delete, ID: b},
	}

	live := crdt.New()
	checkpoints := make([]string, 0, len(operations))
	for _, operation := range operations {
		if err := live.Apply(operation); err != nil {
			t.Fatal(err)
		}
		checkpoints = append(checkpoints, live.Text())
	}

	for index := range operations {
		replayed, err := BuildAt(nil, operations[:index+1])
		if err != nil {
			t.Fatalf("checkpoint %d: %v", index+1, err)
		}
		if got, want := replayed.Text(), checkpoints[index]; got != want {
			t.Fatalf("checkpoint %d text = %q, want %q", index+1, got, want)
		}
	}
}

func TestBuildAtReplaysSnapshotAndTombstones(t *testing.T) {
	a := crdt.CharID{SiteID: "snapshot", Counter: 1}
	b := crdt.CharID{SiteID: "snapshot", Counter: 2}
	snapshotDocument := crdt.New()
	if err := snapshotDocument.Apply(crdt.Operation{Type: crdt.Insert, ID: a, Value: 'A'}); err != nil {
		t.Fatal(err)
	}
	if err := snapshotDocument.Apply(crdt.Operation{Type: crdt.Insert, ID: b, Value: 'B', LeftID: &a}); err != nil {
		t.Fatal(err)
	}
	if err := snapshotDocument.Apply(crdt.Operation{Type: crdt.Delete, ID: a}); err != nil {
		t.Fatal(err)
	}

	c := crdt.CharID{SiteID: "snapshot", Counter: 3}
	replayed, err := BuildAt(snapshotDocument.Snapshot(), []crdt.Operation{{
		Type:   crdt.Insert,
		ID:     c,
		Value:  'C',
		LeftID: &b,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := replayed.Text(), "BC"; got != want {
		t.Fatalf("replayed text = %q, want %q", got, want)
	}
}
