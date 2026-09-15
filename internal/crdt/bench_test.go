package crdt

import (
	"fmt"
	"testing"
)

var benchmarkSizes = []int{1_000, 10_000, 100_000, 1_000_000}
var benchmarkDeleteRatios = []int{0, 30, 70}

func BenchmarkApply(b *testing.B) {
	for _, total := range benchmarkSizes {
		for _, deleteRatio := range benchmarkDeleteRatios {
			operations := benchmarkOperations(total, deleteRatio)
			name := fmt.Sprintf("ops=%d/deletes=%d%%", total, deleteRatio)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(total), "ops/workload")
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					document := New()
					for _, operation := range operations {
						if err := document.Apply(operation); err != nil {
							b.Fatal(err)
						}
					}
				}
			})
		}
	}
}

func BenchmarkText(b *testing.B) {
	for _, total := range benchmarkSizes {
		for _, deleteRatio := range benchmarkDeleteRatios {
			operations := benchmarkOperations(total, deleteRatio)
			document := New()
			for _, operation := range operations {
				if err := document.Apply(operation); err != nil {
					b.Fatal(err)
				}
			}
			name := fmt.Sprintf("ops=%d/deletes=%d%%", total, deleteRatio)
			b.Run(name, func(b *testing.B) {
				b.ReportAllocs()
				b.ReportMetric(float64(total), "ops/workload")
				b.ResetTimer()
				for iteration := 0; iteration < b.N; iteration++ {
					_ = document.Text()
				}
			})
		}
	}
}

func benchmarkOperations(total, deleteRatio int) []Operation {
	operations := make([]Operation, 0, total)
	visible := make([]CharID, 0, total)
	counter := uint64(0)
	for index := 0; index < total; index++ {
		if len(visible) > 0 && (index+deleteRatio)%100 < deleteRatio {
			targetIndex := (index*17 + deleteRatio) % len(visible)
			operations = append(operations, Operation{Type: Delete, ID: visible[targetIndex]})
			visible = append(visible[:targetIndex], visible[targetIndex+1:]...)
			continue
		}

		counter++
		id := CharID{SiteID: "bench", Counter: counter}
		operations = append(operations, Operation{
			Type:  Insert,
			ID:    id,
			Value: rune('a' + index%26),
		})
		visible = append(visible, id)
	}
	return operations
}
