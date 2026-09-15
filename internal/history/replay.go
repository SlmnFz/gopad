// Package history contains deterministic replay helpers shared by the live
// room loader and the read-only time-travel API.
package history

import (
	"fmt"

	"github.com/local/gopad/internal/crdt"
)

// BuildAt reconstructs a document from a materialized snapshot followed by
// the operations after that snapshot. The order is part of the durable
// server history and must be preserved exactly.
func BuildAt(snapshot []crdt.Char, ops []crdt.Operation) (*crdt.Document, error) {
	document := crdt.New()
	for _, char := range snapshot {
		if err := document.Apply(crdt.Operation{
			Type:    crdt.Insert,
			ID:      char.ID,
			Value:   char.Value,
			LeftID:  char.LeftID,
			RightID: char.RightID,
		}); err != nil {
			return nil, fmt.Errorf("apply snapshot insert: %w", err)
		}
		if char.Deleted {
			if err := document.Apply(crdt.Operation{Type: crdt.Delete, ID: char.ID}); err != nil {
				return nil, fmt.Errorf("apply snapshot tombstone: %w", err)
			}
		}
	}
	for index, operation := range ops {
		if err := document.Apply(operation); err != nil {
			return nil, fmt.Errorf("replay operation %d: %w", index+1, err)
		}
	}
	return document, nil
}
