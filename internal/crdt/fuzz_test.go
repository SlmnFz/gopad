package crdt

import (
	"fmt"
	"testing"
)

func FuzzTwoReplicaConverge(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("a"))
	f.Add([]byte("delete-then-insert"))
	f.Add([]byte("0123456789abcdef"))

	f.Fuzz(func(t *testing.T, data []byte) {
		operations := operationsFromBytes(data)
		for orderIndex, ordered := range [][]Operation{operations, reverseOperations(operations)} {
			replicas := []*Document{New(), New()}
			for replicaIndex, document := range replicas {
				if err := applyWithDependencyRetry(document, ordered); err != nil {
					t.Fatalf("order=%d replica=%d operations=%d: %v", orderIndex, replicaIndex, len(ordered), err)
				}
			}
			if replicas[0].Text() != replicas[1].Text() || !sameSnapshot(replicas[0].Snapshot(), replicas[1].Snapshot()) {
				t.Fatalf("order=%d text=%q/%q snapshot diverged for %d operations", orderIndex, replicas[0].Text(), replicas[1].Text(), len(ordered))
			}
		}
	})
}

func operationsFromBytes(data []byte) []Operation {
	if len(data) > 128 {
		data = data[:128]
	}
	documents := []*Document{New(), New()}
	counters := []uint64{0, 0}
	operations := make([]Operation, 0, len(data))
	for index, value := range data {
		replica := int(value & 1)
		snapshot := documents[replica].Snapshot()
		if len(snapshot) > 0 && value%5 < 2 {
			operation := Operation{Type: Delete, ID: snapshot[int(value>>1)%len(snapshot)].ID}
			if err := documents[replica].Apply(operation); err != nil {
				panic(fmt.Sprintf("fuzz generator produced invalid delete at %d: %v", index, err))
			}
			operations = append(operations, operation)
			continue
		}

		counters[replica]++
		operation := Operation{
			Type:  Insert,
			ID:    CharID{SiteID: fmt.Sprintf("fuzz-%d", replica), Counter: counters[replica]},
			Value: rune('a' + value%26),
		}
		if len(snapshot) > 0 && value%4 != 0 {
			parent := snapshot[int(value>>2)%len(snapshot)].ID
			operation.LeftID = &parent
		}
		if err := documents[replica].Apply(operation); err != nil {
			panic(fmt.Sprintf("fuzz generator produced invalid insert at %d: %v", index, err))
		}
		operations = append(operations, operation)
	}
	return operations
}

func reverseOperations(operations []Operation) []Operation {
	reversed := make([]Operation, len(operations))
	for index, operation := range operations {
		reversed[len(operations)-index-1] = operation
	}
	return reversed
}
