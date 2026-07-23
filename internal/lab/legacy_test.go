package lab

import "testing"

func TestLegacyReadDoesNotAdvanceEvacuation(t *testing.T) {
	model := NewLegacy()
	var snapshot Snapshot
	for i := 1; i < 200; i++ {
		snapshot = model.Apply(Operation{Kind: "insert", Key: i, Value: i})
		if snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
			break
		}
	}
	if !snapshot.Legacy.Growing {
		t.Fatal("test did not reach an observable grow")
	}
	before := snapshot.Legacy.Nevacuate
	after := model.Apply(Operation{Kind: "read", Key: 1})
	if after.Legacy.Nevacuate != before {
		t.Fatalf("read advanced nevacuate: %d -> %d", before, after.Legacy.Nevacuate)
	}
}

func TestLegacyMutationCompletesActiveGrowWithinNOperations(t *testing.T) {
	model := NewLegacy()
	var snapshot Snapshot
	for i := 1; i < 200; i++ {
		snapshot = model.Apply(Operation{Kind: "insert", Key: i, Value: i})
		if snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
			break
		}
	}
	n := snapshot.Legacy.OldBucketCount
	if n == 0 {
		t.Fatal("test did not start grow")
	}
	for i := 0; i < n && snapshot.Legacy.Growing; i++ {
		// Updating an existing key progresses grow without increasing count,
		// so the test cannot accidentally trigger the next growth cycle.
		snapshot = model.Apply(Operation{Kind: "insert", Key: 1, Value: 1000 + i})
	}
	if snapshot.Legacy.Growing {
		t.Fatalf("grow did not complete within %d mutating operations", n)
	}
}
