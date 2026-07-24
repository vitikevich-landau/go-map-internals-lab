package lab

import "testing"

// TestLegacyReadDoesNotAdvanceEvacuation проверяет главную гарантию чтения:
// поиск может обращаться к oldBuckets, но никогда не выполняет growWork.
func TestLegacyReadDoesNotAdvanceEvacuation(t *testing.T) {
	model, snapshot := reachObservableLegacyGrow(t)
	beforeRead := snapshot.Legacy.Nevacuate

	afterRead := model.Apply(Operation{Kind: OperationRead, Key: 1})
	if afterRead.Legacy.Nevacuate != beforeRead {
		t.Fatalf(
			"чтение изменило nevacuate: было %d, стало %d",
			beforeRead,
			afterRead.Legacy.Nevacuate,
		)
	}
}

// TestLegacyMutationCompletesActiveGrowWithinNOperations проверяет верхнюю
// границу прогресса: для N старых бакетов не потребуется больше N изменяющих
// операций, даже если целевой бакет и nevacuate часто совпадают.
func TestLegacyMutationCompletesActiveGrowWithinNOperations(t *testing.T) {
	model, snapshot := reachObservableLegacyGrow(t)
	oldBucketCount := snapshot.Legacy.OldBucketCount

	for operationIndex := 0;
		operationIndex < oldBucketCount && snapshot.Legacy.Growing;
		operationIndex++ {
		// Обновление существующего ключа продвигает grow, но не увеличивает count,
		// поэтому тест случайно не запустит следующий цикл роста.
		snapshot = model.Apply(Operation{
			Kind:  OperationInsert,
			Key:   1,
			Value: 1000 + operationIndex,
		})
	}

	if snapshot.Legacy.Growing {
		t.Fatalf(
			"эвакуация не завершилась за %d изменяющих операций",
			oldBucketCount,
		)
	}
}

// reachObservableLegacyGrow заполняет модель до состояния, в котором одновременно
// видны новый массив, oldBuckets и хотя бы четыре старых бакета.
func reachObservableLegacyGrow(t *testing.T) (*Legacy, Snapshot) {
	t.Helper()

	model := NewLegacy()
	var snapshot Snapshot
	for key := 1; key < 200; key++ {
		snapshot = model.Apply(Operation{
			Kind:  OperationInsert,
			Key:   key,
			Value: key,
		})
		if snapshot.Legacy != nil &&
			snapshot.Legacy.Growing &&
			snapshot.Legacy.OldBucketCount >= 4 {
			return model, snapshot
		}
	}

	t.Fatal("не удалось получить наблюдаемое состояние grow")
	return nil, Snapshot{}
}
