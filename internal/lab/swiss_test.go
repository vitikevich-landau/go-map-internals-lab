package lab

import "testing"

func TestSwissGrowthMilestones(t *testing.T) {
	model := NewSwiss(32)
	var snapshot Snapshot

	for i := 1; i <= 8; i++ {
		snapshot = model.Apply(Operation{Kind: "insert", Key: i, Value: i * 10})
	}
	if len(snapshot.Tables) != 1 || snapshot.Tables[0].ID != "small" || snapshot.Tables[0].Capacity != 8 {
		t.Fatalf("8 entries must still use the small group: %+v", snapshot.Tables)
	}

	snapshot = model.Apply(Operation{Kind: "insert", Key: 9, Value: 90})
	if len(snapshot.Tables) != 1 || snapshot.Tables[0].Capacity != 16 {
		t.Fatalf("9th entry must create a 16-slot table: %+v", snapshot.Tables)
	}

	for i := 10; i <= 15; i++ {
		snapshot = model.Apply(Operation{Kind: "insert", Key: i, Value: i * 10})
	}
	if snapshot.Tables[0].Capacity != 32 {
		t.Fatalf("15th entry must grow a 16-slot table to 32 slots: %+v", snapshot.Tables)
	}

	for i := 16; i <= 29; i++ {
		snapshot = model.Apply(Operation{Kind: "insert", Key: i, Value: i * 10})
	}
	if len(snapshot.Directory) != 2 || len(snapshot.Tables) != 2 {
		t.Fatalf("a full max-size teaching table must split: directory=%d tables=%d", len(snapshot.Directory), len(snapshot.Tables))
	}
}

func TestSwissReadDoesNotChangeShape(t *testing.T) {
	model := NewSwiss(32)
	for i := 1; i <= 20; i++ {
		model.Apply(Operation{Kind: "insert", Key: i, Value: i})
	}
	before := model.Snapshot()
	after := model.Apply(Operation{Kind: "read", Key: 7})
	if len(before.Directory) != len(after.Directory) || len(before.Tables) != len(after.Tables) {
		t.Fatalf("read changed Swiss structure")
	}
	for i := range before.Tables {
		if before.Tables[i].Capacity != after.Tables[i].Capacity || before.Tables[i].Used != after.Tables[i].Used {
			t.Fatalf("read changed table %d", i)
		}
	}
}

func TestSwissDeleteAndRewrite(t *testing.T) {
	model := NewSwiss(32)
	for i := 1; i <= 18; i++ {
		model.Apply(Operation{Kind: "insert", Key: i, Value: i})
	}
	deleted := model.Apply(Operation{Kind: "delete", Key: 9})
	if deleted.Stats[0].Value != "17" {
		t.Fatalf("delete did not decrement used: %+v", deleted.Stats[0])
	}
	restored := model.Apply(Operation{Kind: "insert", Key: 9, Value: 900})
	if restored.Stats[0].Value != "18" {
		t.Fatalf("rewrite did not restore used: %+v", restored.Stats[0])
	}
}
