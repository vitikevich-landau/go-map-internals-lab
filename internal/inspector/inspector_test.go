package inspector

import (
	"testing"

	"go-map-internals-lab/internal/lab"
)

// TestInspectorSmoke проверяет общий контракт обоих build-вариантов инспектора:
// после обычных записей должен получиться снимок реального runtime с заголовочной
// статистикой.
func TestInspectorSmoke(t *testing.T) {
	inspector := New()
	for key := 1; key <= 40; key++ {
		inspector.Apply(lab.Operation{
			Kind:  lab.OperationInsert,
			Key:   key,
			Value: key * 10,
		})
	}

	snapshot := inspector.Snapshot()
	if len(snapshot.Stats) == 0 {
		t.Fatal("unsafe-инспектор не вернул статистику заголовка map")
	}
	if snapshot.Mode != lab.ModeReal {
		t.Fatalf("неожиданный режим снимка: %q", snapshot.Mode)
	}
}
