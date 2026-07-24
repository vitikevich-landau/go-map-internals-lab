package lab

import (
	"fmt"
	"math"
)

const legacyBucketSlots = 8

// legacyBucket моделирует один bmap классической реализации Go.
//
// В основном бакете восемь слотов. Если они заканчиваются, следующий bmap
// добавляется в overflow-цепочку. В модели сама цепочка хранится срезом в
// legacyArray.Chains: элемент с индексом 0 — основной бакет, остальные — overflow.
type legacyBucket struct {
	Slots [legacyBucketSlots]*swissEntry
}

// legacyArray представляет один массив buckets или oldbuckets.
//
// Chains содержит по одной overflow-цепочке на каждый основной бакет.
// Evacuated имеет ту же длину и отмечает старые бакеты, содержимое которых уже
// перенесено в новый массив во время grow.
type legacyArray struct {
	Chains    [][]*legacyBucket
	Evacuated []bool
}

// Legacy моделирует реализацию map с hmap/bmap, overflow-цепочками и
// инкрементальной эвакуацией, которая использовалась до Go 1.24.
//
// Во время обычной работы buckets указывает на единственный активный массив, а
// oldBuckets равен nil. При grow создаётся новый buckets, прежний массив
// сохраняется в oldBuckets, и изменяющие операции постепенно переносят бакеты.
type Legacy struct {
	seed  HashSeed
	count ElementCount
	B     HashDepth

	buckets    *legacyArray
	oldBuckets *legacyArray
	nevacuate  BucketIndex

	trace      []TraceStep
	events     EventCount
	highlights map[UIObjectID]bool
}

// NewLegacy создаёт пустую классическую map с B=0, то есть с одним основным
// бакетом на восемь слотов.
func NewLegacy() *Legacy {
	model := &Legacy{
		seed:       0xbb67ae8584caa73b,
		B:          0,
		buckets:    newLegacyArray(1),
		highlights: make(map[UIObjectID]bool),
		trace: []TraceStep{{
			Phase:  TraceReady,
			Title:  "Один пустой бакет",
			Detail: "Классическая map начинает с 2^B бакетов; при B=0 это один бакет на 8 слотов.",
			Tone:   TraceInfo,
		}},
	}
	return model
}

// Apply выполняет одну пользовательскую операцию и возвращает новый снимок.
//
// Перед каждым действием предыдущая трассировка и подсветка очищаются, но сама
// map сохраняет накопленное состояние.
func (l *Legacy) Apply(op Operation) Snapshot {
	l.trace = nil
	l.highlights = make(map[UIObjectID]bool)
	l.events++

	switch op.Kind {
	case OperationRead:
		l.read(op.Key)
	case OperationDelete:
		l.delete(op.Key)
	default:
		l.insert(op.Key, op.Value)
	}
	return l.Snapshot()
}

// insert записывает новую пару или обновляет существующее значение.
//
// Запись состоит из трёх смысловых этапов:
//  1. при необходимости запускается новый grow;
//  2. уже активный grow получает порцию growWork;
//  3. пара обновляется либо помещается в новый массив buckets.
func (l *Legacy) insert(key MapKey, value MapValue) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "запись")

	// Обновление существующего ключа само по себе не запускает следующий grow,
	// потому что count не увеличится. Но если эвакуация уже активна, эта запись
	// всё равно обязана выполнить growWork.
	_, exists := l.find(key, hash)
	if !exists && l.oldBuckets == nil && l.overLoadFactor(l.count+1) {
		l.startGrow()
	}
	if l.oldBuckets != nil {
		l.growWork(hash)
	}

	// growWork мог переместить искомый ключ из oldBuckets в buckets, поэтому после
	// него местоположение ищется заново.
	if location, ok := l.find(key, hash); ok {
		location.entry.Value = value
		l.highlights[location.target] = true
		l.trace = append(l.trace, TraceStep{
			Phase:  TraceUpdate,
			Title:  "Существующая пара обновлена",
			Detail: fmt.Sprintf("Ключ %d уже есть; count не меняется. Активная эвакуация всё равно получила growWork.", key),
			Tone:   TraceSuccess,
			Target: location.target,
		})
		return
	}

	bucketIndex := legacyBucketIndex(hash, len(l.buckets.Chains))
	overflowIndex, slotIndex := insertLegacy(
		&l.buckets.Chains[bucketIndex],
		&swissEntry{Key: key, Value: value, Hash: hash},
	)
	l.count++

	target := legacyTarget(LegacyNewBuckets, bucketIndex, overflowIndex, slotIndex)
	l.highlights[target] = true
	detail := fmt.Sprintf(
		"Младшие B=%d бит выбрали бакет %d; пара записана в bmap #%d, слот %d.",
		l.B,
		bucketIndex,
		overflowIndex,
		slotIndex,
	)
	if overflowIndex > 0 {
		detail += " Это overflow-bucket: длинная цепочка делает доступ дороже."
	}
	l.trace = append(l.trace, TraceStep{
		Phase:  TraceInsert,
		Title:  "Пара записана в новый массив",
		Detail: detail,
		Tone:   TraceSuccess,
		Target: target,
	})
}

// read ищет ключ, но принципиально не выполняет growWork.
//
// Пока старый бакет не эвакуирован, ключ нужно искать в oldBuckets. После
// эвакуации тот же хеш направляет чтение уже в новый массив buckets.
func (l *Legacy) read(key MapKey) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "чтение")

	if l.oldBuckets != nil {
		oldIndex := legacyBucketIndex(hash, len(l.oldBuckets.Chains))
		if !l.oldBuckets.Evacuated[oldIndex] {
			l.trace = append(l.trace, TraceStep{
				Phase:  TraceRoute,
				Title:  "Чтение идёт в oldbuckets",
				Detail: fmt.Sprintf("Старый бакет %d ещё не эвакуирован. Чтение ищет там и не двигает nevacuate.", oldIndex),
				Tone:   TraceInfo,
				Target: legacyBucketTarget(LegacyOldBuckets, oldIndex),
			})
		} else {
			newIndex := legacyBucketIndex(hash, len(l.buckets.Chains))
			l.trace = append(l.trace, TraceStep{
				Phase:  TraceRoute,
				Title:  "Старый бакет уже эвакуирован",
				Detail: fmt.Sprintf("Маркер evacuated в бакете %d отправляет поиск в новый массив. Чтение по-прежнему ничего не переносит.", oldIndex),
				Tone:   TraceInfo,
				Target: legacyBucketTarget(LegacyNewBuckets, newIndex),
			})
		}
	}

	location, ok := l.find(key, hash)
	if ok {
		l.highlights[location.target] = true
		l.trace = append(l.trace, TraceStep{
			Phase:  TraceRead,
			Title:  "Ключ найден",
			Detail: fmt.Sprintf("Получено значение %d. nevacuate остался равен %d.", location.entry.Value, l.nevacuate),
			Tone:   TraceSuccess,
			Target: location.target,
		})
		return
	}

	l.trace = append(l.trace, TraceStep{
		Phase:  TraceRead,
		Title:  "Ключ отсутствует",
		Detail: "Цепочка бакета проверена до конца; состояние map не изменилось.",
		Tone:   TraceWarning,
	})
}

// delete удаляет ключ и, как любая изменяющая операция, помогает активному grow.
func (l *Legacy) delete(key MapKey) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "удаление")

	if l.oldBuckets != nil {
		l.growWork(hash)
	}

	location, ok := l.find(key, hash)
	if !ok {
		l.trace = append(l.trace, TraceStep{
			Phase:  TraceDelete,
			Title:  "Удалять нечего",
			Detail: "Ключ не найден.",
			Tone:   TraceWarning,
		})
		return
	}

	*location.slot = nil
	l.count--
	l.trace = append(l.trace, TraceStep{
		Phase:  TraceDelete,
		Title:  "Слот освобождён",
		Detail: "Удаление, как и запись, выполняет growWork при активном росте. В представлении runtime tophash получает специальный empty-маркер.",
		Tone:   TraceSuccess,
		Target: location.target,
	})
}

// startGrow переключает map в состояние инкрементального роста.
//
// Новый массив становится текущим buckets сразу, но все пары ещё физически лежат
// в oldBuckets. Дальнейшие изменяющие операции постепенно распределят каждый
// старый бакет между двумя возможными новыми бакетами.
func (l *Legacy) startGrow() {
	oldCount := BucketCount(len(l.buckets.Chains))
	l.oldBuckets = l.buckets
	l.buckets = newLegacyArray(oldCount * 2)
	l.B++
	l.nevacuate = 0

	l.trace = append(l.trace, TraceStep{
		Phase:  TraceGrow,
		Title:  fmt.Sprintf("Начался grow: %d → %d бакетов", oldCount, oldCount*2),
		Detail: "buckets уже указывает на новый массив, oldbuckets удерживает старый. Данные физически распределены между двумя массивами до завершения эвакуации.",
		Tone:   TraceWarning,
		Target: "legacy-arrays",
		Formula: fmt.Sprintf(
			"проверка нагрузки: count=%d > 6.5 × %d = %.1f",
			l.count+1,
			oldCount,
			6.5*float64(oldCount),
		),
	})
}

// growWork повторяет важный порядок действий runtime/map_noswiss.go:
//  1. эвакуируется старый бакет, выбранный хешем текущей операции;
//  2. если grow ещё не завершён, эвакуируется бакет с индексом nevacuate.
//
// Эти индексы могут совпасть, а один из бакетов может быть уже готов. Поэтому
// изменяющая операция гарантирует прогресс, но не гарантирует два новых переноса.
func (l *Legacy) growWork(hash FullHash) {
	if l.oldBuckets == nil {
		return
	}

	target := legacyBucketIndex(hash, len(l.oldBuckets.Chains))
	l.trace = append(l.trace, TraceStep{
		Phase:  TraceGrowWork,
		Title:  "Запись платит «налог на рост»",
		Detail: fmt.Sprintf("Сначала целевой старый бакет %d, затем текущий nevacuate=%d. Если это один и тот же или уже готовый бакет, новых переносов будет меньше двух.", target, l.nevacuate),
		Tone:   TraceWarning,
	})

	l.evacuate(target, "целевой бакет операции")
	if l.oldBuckets != nil {
		l.evacuate(l.nevacuate, "плановый бакет nevacuate")
	}
}

// evacuate переносит всю overflow-цепочку одного старого бакета.
//
// При удвоении массива старый бакет i разделяется только на два направления:
// X остаётся по индексу i, Y уходит в i+oldBucketCount. Направление определяет
// один новый бит хеша — hash & oldBucketCount.
func (l *Legacy) evacuate(oldIndex BucketIndex, reason EvacuationReason) {
	if l.oldBuckets == nil ||
		oldIndex < 0 ||
		oldIndex >= len(l.oldBuckets.Chains) ||
		l.oldBuckets.Evacuated[oldIndex] {
		l.trace = append(l.trace, TraceStep{
			Phase:  TraceEvacuateSkip,
			Title:  "Повторный перенос не нужен",
			Detail: fmt.Sprintf("Бакет %d (%s) уже эвакуирован.", oldIndex, reason),
			Tone:   TraceInfo,
			Target: legacyBucketTarget(LegacyOldBuckets, oldIndex),
		})
		return
	}

	oldCount := BucketCount(len(l.oldBuckets.Chains))
	movedX, movedY := ElementCount(0), ElementCount(0)

	// Эвакуируется не только основной bmap, а вся его overflow-цепочка.
	for _, bucket := range l.oldBuckets.Chains[oldIndex] {
		for _, entry := range bucket.Slots {
			if entry == nil {
				continue
			}

			newIndex := oldIndex
			if entry.Hash&FullHash(oldCount) != 0 {
				newIndex += oldCount
				movedY++
			} else {
				movedX++
			}
			insertLegacy(&l.buckets.Chains[newIndex], entry)
		}
	}

	l.oldBuckets.Evacuated[oldIndex] = true
	l.trace = append(l.trace, TraceStep{
		Phase:  TraceEvacuate,
		Title:  fmt.Sprintf("Старый бакет %d эвакуирован", oldIndex),
		Detail: fmt.Sprintf("%s: X остаётся в новом бакете %d (%d пар), Y уходит в %d (%d пар). Решает новый бит hash & oldBucketCount.", reason, oldIndex, movedX, oldIndex+oldCount, movedY),
		Tone:   TraceSuccess,
		Target: legacyBucketTarget(LegacyOldBuckets, oldIndex),
		Formula: fmt.Sprintf(
			"newbit = %d; hash & newbit → X(0) или Y(1)",
			oldCount,
		),
	})

	// nevacuate всегда указывает на первый ещё не перенесённый индекс. Если
	// целевая операция заранее эвакуировала несколько бакетов, счётчик перескочит
	// через всю уже готовую последовательность.
	if oldIndex == l.nevacuate {
		for l.nevacuate < oldCount && l.oldBuckets.Evacuated[l.nevacuate] {
			l.nevacuate++
		}
	}

	// Когда последовательный счётчик дошёл до конца старого массива, ссылку на
	// oldBuckets можно удалить: все пары уже доступны только через новый buckets.
	if l.nevacuate == oldCount {
		l.oldBuckets = nil
		l.nevacuate = 0
		l.trace = append(l.trace, TraceStep{
			Phase:  TraceComplete,
			Title:  "Эвакуация завершена",
			Detail: "oldbuckets обнулён. Старый массив становится недостижимым и позднее освобождается сборщиком мусора.",
			Tone:   TraceSuccess,
			Target: "legacy-arrays",
		})
	}
}

// overLoadFactor проверяет учебное условие обычного роста классической map.
//
// Первая часть не позволяет расти раньше заполнения одного бакета. Вторая часть
// записывает условие count > 6.5 × 2^B без вычислений с float.
func (l *Legacy) overLoadFactor(nextCount ElementCount) bool {
	bucketCount := BucketCount(1 << l.B)
	return nextCount > legacyBucketSlots && nextCount*2 > 13*bucketCount
}

// legacyLocation хранит найденную пару, адрес слота для возможного удаления и
// идентификатор физического места для подсветки в интерфейсе.
type legacyLocation struct {
	entry  *swissEntry
	slot   **swissEntry
	target UIObjectID
}

// find выбирает правильное поколение массива и ищет ключ в его overflow-цепочке.
func (l *Legacy) find(key MapKey, hash FullHash) (legacyLocation, bool) {
	if l.oldBuckets != nil {
		oldIndex := legacyBucketIndex(hash, len(l.oldBuckets.Chains))
		if !l.oldBuckets.Evacuated[oldIndex] {
			if location, ok := findLegacy(
				l.oldBuckets.Chains[oldIndex],
				key,
				LegacyOldBuckets,
				oldIndex,
			); ok {
				return location, true
			}
			return legacyLocation{}, false
		}
	}

	newIndex := legacyBucketIndex(hash, len(l.buckets.Chains))
	return findLegacy(
		l.buckets.Chains[newIndex],
		key,
		LegacyNewBuckets,
		newIndex,
	)
}

// findLegacy последовательно проверяет основной bmap и все overflow-bmap одной
// цепочки.
func findLegacy(
	chain []*legacyBucket,
	key MapKey,
	generation LegacyGeneration,
	bucketIndex BucketIndex,
) (legacyLocation, bool) {
	for overflowIndex, bucket := range chain {
		for slotIndex := range bucket.Slots {
			entry := bucket.Slots[slotIndex]
			if entry != nil && entry.Key == key {
				return legacyLocation{
					entry:  entry,
					slot:   &bucket.Slots[slotIndex],
					target: legacyTarget(generation, bucketIndex, overflowIndex, slotIndex),
				}, true
			}
		}
	}
	return legacyLocation{}, false
}

// insertLegacy занимает первый свободный слот цепочки. Если вся цепочка полна,
// создаётся новый overflow-bmap и пара записывается в его первый слот.
func insertLegacy(
	chain *[]*legacyBucket,
	entry *swissEntry,
) (OverflowIndex, SlotIndex) {
	for overflowIndex, bucket := range *chain {
		for slotIndex := range bucket.Slots {
			if bucket.Slots[slotIndex] == nil {
				bucket.Slots[slotIndex] = entry
				return overflowIndex, slotIndex
			}
		}
	}

	newBucket := &legacyBucket{}
	*chain = append(*chain, newBucket)
	newBucket.Slots[0] = entry
	return len(*chain) - 1, 0
}

// newLegacyArray создаёт массив основных бакетов и по одной начальной bmap в
// каждой цепочке.
func newLegacyArray(size BucketCount) *legacyArray {
	array := &legacyArray{
		Chains:    make([][]*legacyBucket, size),
		Evacuated: make([]bool, size),
	}
	for bucketIndex := range array.Chains {
		// Запас ёмкости делает добавление overflow-бакетов стабильным для учебных
		// сценариев. Интерфейсу не требуются сотни элементов в одной цепочке.
		array.Chains[bucketIndex] = make([]*legacyBucket, 1, 32)
		array.Chains[bucketIndex][0] = &legacyBucket{}
	}
	return array
}

// legacyBucketIndex выбирает основной бакет по младшим битам хеша.
// Количество бакетов всегда является степенью двойки, поэтому остаток заменяется
// дешёвой маской count-1.
func legacyBucketIndex(hash FullHash, count BucketCount) BucketIndex {
	return BucketIndex(hash & FullHash(count-1))
}

// traceHash добавляет в учебную трассировку разбор выбора legacy-бакета.
func (l *Legacy) traceHash(key MapKey, hash FullHash, action string) {
	l.trace = append(l.trace, TraceStep{
		Phase: TraceHash,
		Title: fmt.Sprintf(
			"Ключ %d хешируется для операции «%s»",
			key,
			action,
		),
		Detail: fmt.Sprintf(
			"hash = 0x%016x. Младшие B=%d бит выбирают бакет; верхний байт служит tophash-фильтром внутри bmap.",
			hash,
			l.B,
		),
		Formula: fmt.Sprintf(
			"bucket = hash & (2^B − 1) = %d",
			legacyBucketIndex(hash, 1<<l.B),
		),
		Tone: TraceInfo,
	})
}

// Snapshot собирает полное представление legacy-модели для браузера.
func (l *Legacy) Snapshot() Snapshot {
	growing := l.oldBuckets != nil
	oldCount := BucketCount(0)
	progress := "—"
	if growing {
		oldCount = len(l.oldBuckets.Chains)
		progress = fmt.Sprintf("%d/%d", l.nevacuate, oldCount)
	}

	overflow := countOverflow(l.buckets)
	snapshot := Snapshot{
		Mode:     ModeLegacy,
		Title:    "Классическая map: бакеты и эвакуация",
		Subtitle: "До Go 1.24 · hmap/bmap · tophash · overflow · oldbuckets · nevacuate",
		Notice:   "Изменяющая операция вызывает growWork: целевой старый бакет + текущий nevacuate. Это не означает два новых бакета на каждую запись.",
		Stats: []Stat{
			{Label: "count", Value: fmt.Sprint(l.count), Hint: "Число пар ключ → значение"},
			{Label: "B", Value: fmt.Sprintf("%d → %d бакетов", l.B, 1<<l.B), Hint: "Количество бакетов равно 2^B"},
			{Label: "oldbuckets", Value: map[bool]string{true: "активен", false: "nil"}[growing], Hint: "Старый массив существует только во время grow"},
			{Label: "nevacuate", Value: progress, Hint: "Все индексы меньше счётчика уже перенесены"},
			{Label: "overflow", Value: fmt.Sprint(overflow), Hint: "Дополнительные bmap в цепочках"},
		},
		Trace:      l.trace,
		EventCount: l.events,
		Legacy: &LegacyView{
			B:              l.B,
			NewBuckets:     l.bucketViews(l.buckets, LegacyNewBuckets),
			Nevacuate:      l.nevacuate,
			OldBucketCount: oldCount,
			Growing:        growing,
		},
	}

	if growing {
		snapshot.Legacy.OldBuckets = l.bucketViews(l.oldBuckets, LegacyOldBuckets)
		minimum := int(math.Ceil(float64(oldCount) / 2))
		snapshot.ScaleNote = fmt.Sprintf(
			"Для %d старых бакетов после старта нужно от %d до %d изменяющих операций; точное число зависит от хешей целевых ключей. Чтения дают 0 прогресса.",
			oldCount,
			minimum,
			oldCount,
		)
	} else {
		snapshot.ScaleNote = "Обычный рост запускается при count > 8 и count > 6.5 × 2^B. Отдельно runtime может делать same-size grow из-за избытка overflow-бакетов."
	}
	return snapshot
}

// bucketViews преобразует внутренний массив бакетов в нейтральную JSON-модель.
func (l *Legacy) bucketViews(
	array *legacyArray,
	generation LegacyGeneration,
) []BucketView {
	if array == nil {
		return nil
	}

	views := make([]BucketView, 0, len(array.Chains))
	for bucketIndex, chain := range array.Chains {
		view := BucketView{
			Index:     bucketIndex,
			Evacuated: array.Evacuated[bucketIndex],
		}
		for overflowIndex, bucket := range chain {
			slots := make([]SlotView, 0, legacyBucketSlots)
			for slotIndex, entry := range bucket.Slots {
				state := SlotEmpty
				if view.Evacuated {
					state = SlotEvacuated
				} else if entry != nil {
					state = SlotFull
				}

				physicalID := legacyTarget(
					generation,
					bucketIndex,
					overflowIndex,
					slotIndex,
				)
				slot := SlotView{
					Index:      slotIndex,
					State:      state,
					Control:    "—",
					PhysicalID: physicalID,
					Highlight:  l.highlights[physicalID],
				}
				if entry != nil {
					slot.Control = fmt.Sprintf("0x%02x", byte(entry.Hash>>56))
					slot.Key = intPtr(entry.Key)
					slot.Value = intPtr(entry.Value)
				}
				slots = append(slots, slot)
			}
			view.Chain = append(view.Chain, slots)
		}
		views = append(views, view)
	}
	return views
}

// countOverflow считает дополнительные bmap, не включая основные бакеты.
func countOverflow(array *legacyArray) ElementCount {
	count := ElementCount(0)
	for _, chain := range array.Chains {
		if len(chain) > 1 {
			count += len(chain) - 1
		}
	}
	return count
}

// legacyBucketTarget возвращает идентификатор основного бакета для подсветки.
func legacyBucketTarget(
	generation LegacyGeneration,
	bucket BucketIndex,
) UIObjectID {
	return fmt.Sprintf("%s-b%d", generation, bucket)
}

// legacyTarget возвращает идентификатор конкретного физического слота.
func legacyTarget(
	generation LegacyGeneration,
	bucket BucketIndex,
	overflow OverflowIndex,
	slot SlotIndex,
) UIObjectID {
	return fmt.Sprintf("%s-b%d-o%d-s%d", generation, bucket, overflow, slot)
}
