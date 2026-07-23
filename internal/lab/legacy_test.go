package lab

import "testing"

func TestLegacyReadDoesNotAdvanceEvacuation(t *testing.T) {
	model := NewLegacy()
	var snapshot Snapshot
	for key := 1; key < 200; key++ {
		snapshot = model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key})
		if snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
			break
		}
	}
	if !snapshot.Legacy.Growing {
		t.Fatal("test did not reach an observable grow")
	}
	before := snapshot.Legacy.Nevacuate
	after := model.Apply(Operation{Kind: OperationRead, Key: 1})
	if after.Legacy.Nevacuate != before {
		t.Fatalf("read advanced nevacuate: %d -> %d", before, after.Legacy.Nevacuate)
	}
}

func TestLegacyMutationCompletesActiveGrowWithinNOperations(t *testing.T) {
	model := NewLegacy()
	var snapshot Snapshot
	for key := 1; key < 200; key++ {
		snapshot = model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key})
		if snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
			break
		}
	}
	n := snapshot.Legacy.OldBucketCount
	if n == 0 {
		t.Fatal("test did not start grow")
	}
	for operationIndex := 0; operationIndex < n && snapshot.Legacy.Growing; operationIndex++ {
		// Обновление существующего ключа продвигает grow, но не увеличивает count,
		// поэтому тест случайно не запустит следующий цикл роста.
		snapshot = model.Apply(Operation{
			Kind:  OperationInsert,
			Key:   1,
			Value: 1000 + operationIndex,
		})
	}
	if snapshot.Legacy.Growing {
		t.Fatalf("grow did not complete within %d mutating operations", n)
	}
}
