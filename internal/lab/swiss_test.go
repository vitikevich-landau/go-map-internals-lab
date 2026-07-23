package lab

import "testing"

func TestSwissGrowthMilestones(t *testing.T) {
	model := NewSwiss(32)
	var snapshot Snapshot

	for key := 1; key <= 8; key++ {
		snapshot = model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key * 10})
	}
	if len(snapshot.Tables) != 1 || snapshot.Tables[0].ID != "small" || snapshot.Tables[0].Capacity != 8 {
		t.Fatalf("8 entries must still use the small group: %+v", snapshot.Tables)
	}

	snapshot = model.Apply(Operation{Kind: OperationInsert, Key: 9, Value: 90})
	if len(snapshot.Tables) != 1 || snapshot.Tables[0].Capacity != 16 {
		t.Fatalf("9th entry must create a 16-slot table: %+v", snapshot.Tables)
	}

	for key := 10; key <= 15; key++ {
		snapshot = model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key * 10})
	}
	if snapshot.Tables[0].Capacity != 32 {
		t.Fatalf("15th entry must grow a 16-slot table to 32 slots: %+v", snapshot.Tables)
	}

	for key := 16; key <= 29; key++ {
		snapshot = model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key * 10})
	}
	if len(snapshot.Directory) != 2 || len(snapshot.Tables) != 2 {
		t.Fatalf("a full max-size teaching table must split: directory=%d tables=%d", len(snapshot.Directory), len(snapshot.Tables))
	}
}

func TestSwissReadDoesNotChangeShape(t *testing.T) {
	model := NewSwiss(32)
	for key := 1; key <= 20; key++ {
		model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key})
	}
	before := model.Snapshot()
	after := model.Apply(Operation{Kind: OperationRead, Key: 7})
	if len(before.Directory) != len(after.Directory) || len(before.Tables) != len(after.Tables) {
		t.Fatalf("read changed Swiss structure")
	}
	for tableIndex := range before.Tables {
		if before.Tables[tableIndex].Capacity != after.Tables[tableIndex].Capacity || before.Tables[tableIndex].Used != after.Tables[tableIndex].Used {
			t.Fatalf("read changed table %d", tableIndex)
		}
	}
}

func TestSwissDeleteAndRewrite(t *testing.T) {
	model := NewSwiss(32)
	for key := 1; key <= 18; key++ {
		model.Apply(Operation{Kind: OperationInsert, Key: key, Value: key})
	}
	deleted := model.Apply(Operation{Kind: OperationDelete, Key: 9})
	if deleted.Stats[0].Value != "17" {
		t.Fatalf("delete did not decrement used: %+v", deleted.Stats[0])
	}
	restored := model.Apply(Operation{Kind: OperationInsert, Key: 9, Value: 900})
	if restored.Stats[0].Value != "18" {
		t.Fatalf("rewrite did not restore used: %+v", restored.Stats[0])
	}
}
