package lab

import "testing"

// TestSwissGrowthMilestones проверяет все основные переходы учебной модели:
// малая группа → таблица 16 → таблица 32 → split на две таблицы.
func TestSwissGrowthMilestones(t *testing.T) {
	model := NewSwiss(32)

	snapshot := insertSwissRange(model, 1, 8)
	if len(snapshot.Tables) != 1 ||
		snapshot.Tables[0].ID != smallTableID ||
		snapshot.Tables[0].Capacity != 8 {
		t.Fatalf(
			"восемь пар должны оставаться в малой группе: %+v",
			snapshot.Tables,
		)
	}

	snapshot = model.Apply(Operation{Kind: OperationInsert, Key: 9, Value: 90})
	if len(snapshot.Tables) != 1 || snapshot.Tables[0].Capacity != 16 {
		t.Fatalf(
			"девятая пара должна создать таблицу на 16 слотов: %+v",
			snapshot.Tables,
		)
	}

	snapshot = insertSwissRange(model, 10, 15)
	if snapshot.Tables[0].Capacity != 32 {
		t.Fatalf(
			"пятнадцатая пара должна увеличить таблицу до 32 слотов: %+v",
			snapshot.Tables,
		)
	}

	snapshot = insertSwissRange(model, 16, 29)
	if len(snapshot.Directory) != 2 || len(snapshot.Tables) != 2 {
		t.Fatalf(
			"заполненная предельная таблица должна разделиться: directory=%d, tables=%d",
			len(snapshot.Directory),
			len(snapshot.Tables),
		)
	}
}

// TestSwissReadDoesNotChangeShape проверяет, что чтение не меняет directory,
// ёмкости таблиц и количество живых пар.
func TestSwissReadDoesNotChangeShape(t *testing.T) {
	model := NewSwiss(32)
	insertSwissRange(model, 1, 20)

	beforeRead := model.Snapshot()
	afterRead := model.Apply(Operation{Kind: OperationRead, Key: 7})
	if len(beforeRead.Directory) != len(afterRead.Directory) ||
		len(beforeRead.Tables) != len(afterRead.Tables) {
		t.Fatal("чтение изменило форму Swiss map")
	}

	for tableIndex := range beforeRead.Tables {
		beforeTable := beforeRead.Tables[tableIndex]
		afterTable := afterRead.Tables[tableIndex]
		if beforeTable.Capacity != afterTable.Capacity ||
			beforeTable.Used != afterTable.Used {
			t.Fatalf("чтение изменило таблицу %d", tableIndex)
		}
	}
}

// TestSwissDeleteAndRewrite проверяет, что удаление уменьшает общий used, а
// повторная запись того же ключа восстанавливает количество элементов.
func TestSwissDeleteAndRewrite(t *testing.T) {
	model := NewSwiss(32)
	insertSwissRange(model, 1, 18)

	afterDelete := model.Apply(Operation{Kind: OperationDelete, Key: 9})
	if afterDelete.Stats[0].Value != "17" {
		t.Fatalf(
			"удаление не уменьшило used: %+v",
			afterDelete.Stats[0],
		)
	}

	afterRewrite := model.Apply(Operation{
		Kind:  OperationInsert,
		Key:   9,
		Value: 900,
	})
	if afterRewrite.Stats[0].Value != "18" {
		t.Fatalf(
			"повторная запись не восстановила used: %+v",
			afterRewrite.Stats[0],
		)
	}
}

// insertSwissRange последовательно записывает включительный диапазон ключей и
// возвращает снимок после последней операции.
func insertSwissRange(model *Swiss, firstKey, lastKey MapKey) Snapshot {
	var snapshot Snapshot
	for key := firstKey; key <= lastKey; key++ {
		snapshot = model.Apply(Operation{
			Kind:  OperationInsert,
			Key:   key,
			Value: key * 10,
		})
	}
	return snapshot
}
