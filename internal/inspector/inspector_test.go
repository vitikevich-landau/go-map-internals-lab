package inspector

import (
	"testing"

	"go-map-internals-lab/internal/lab"
)

func TestInspectorSmoke(t *testing.T) {
	inspector := New()
	for i := 1; i <= 40; i++ {
		inspector.Apply(lab.Operation{Kind: "insert", Key: i, Value: i * 10})
	}
	snapshot := inspector.Snapshot()
	if len(snapshot.Stats) == 0 {
		t.Fatal("unsafe inspector returned no header statistics")
	}
	if snapshot.Mode != "real" {
		t.Fatalf("unexpected mode %q", snapshot.Mode)
	}
}
