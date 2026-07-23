package lab

import (
	"fmt"
	"math"
)

// legacyBucket models bmap: eight primary slots plus an overflow link. A
// bucket chain is represented as a slice, where index 0 is the primary bmap.
type legacyBucket struct {
	Slots [8]*swissEntry
}

type legacyArray struct {
	Chains    [][]*legacyBucket
	Evacuated []bool
}

// Legacy models the bucket/overflow/oldbuckets implementation used before the
// Swiss Table became the default in Go 1.24.
type Legacy struct {
	seed       uint64
	count      int
	B          int
	buckets    *legacyArray
	oldBuckets *legacyArray
	nevacuate  int
	trace      []TraceStep
	events     int
	highlights map[string]bool
}

func NewLegacy() *Legacy {
	l := &Legacy{
		seed:       0xbb67ae8584caa73b,
		B:          0,
		buckets:    newLegacyArray(1),
		highlights: make(map[string]bool),
		trace: []TraceStep{{
			Phase: "ready", Title: "Один пустой бакет",
			Detail: "Классическая map начинает с 2^B бакетов; при B=0 это один бакет на 8 слотов.",
			Tone:   "info",
		}},
	}
	return l
}

func (l *Legacy) Apply(op Operation) Snapshot {
	l.trace = nil
	l.highlights = make(map[string]bool)
	l.events++
	switch op.Kind {
	case "read":
		l.read(op.Key)
	case "delete":
		l.delete(op.Key)
	default:
		l.insert(op.Key, op.Value)
	}
	return l.Snapshot()
}

func (l *Legacy) insert(key, value int) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "запись")

	// Updating an existing key does not itself trigger a new grow, but an
	// already active grow still receives work from this write.
	_, exists := l.find(key, hash)
	if !exists && l.oldBuckets == nil && l.overLoadFactor(l.count+1) {
		l.startGrow()
	}
	if l.oldBuckets != nil {
		l.growWork(hash)
	}

	if location, ok := l.find(key, hash); ok {
		location.entry.Value = value
		l.highlights[location.target] = true
		l.trace = append(l.trace, TraceStep{
			Phase: "update", Title: "Существующая пара обновлена",
			Detail: fmt.Sprintf("Ключ %d уже есть; count не меняется. Активная эвакуация всё равно получила growWork.", key),
			Tone:   "success", Target: location.target,
		})
		return
	}

	index := int(hash & uint64(len(l.buckets.Chains)-1))
	bucketIndex, slotIndex := insertLegacy(&l.buckets.Chains[index], &swissEntry{Key: key, Value: value, Hash: hash})
	l.count++
	target := legacyTarget("new", index, bucketIndex, slotIndex)
	l.highlights[target] = true
	detail := fmt.Sprintf("Младшие B=%d бит выбрали бакет %d; пара записана в bmap #%d, слот %d.", l.B, index, bucketIndex, slotIndex)
	if bucketIndex > 0 {
		detail += " Это overflow-bucket: длинная цепочка делает доступ дороже."
	}
	l.trace = append(l.trace, TraceStep{
		Phase: "insert", Title: "Пара записана в новый массив",
		Detail: detail, Tone: "success", Target: target,
	})
}

func (l *Legacy) read(key int) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "чтение")
	oldIndex := -1
	if l.oldBuckets != nil {
		oldIndex = int(hash & uint64(len(l.oldBuckets.Chains)-1))
		if !l.oldBuckets.Evacuated[oldIndex] {
			l.trace = append(l.trace, TraceStep{
				Phase: "route", Title: "Чтение идёт в oldbuckets",
				Detail: fmt.Sprintf("Старый бакет %d ещё не эвакуирован. Чтение ищет там и не двигает nevacuate.", oldIndex),
				Tone:   "info", Target: fmt.Sprintf("old-b%d", oldIndex),
			})
		} else {
			l.trace = append(l.trace, TraceStep{
				Phase: "route", Title: "Старый бакет уже эвакуирован",
				Detail: fmt.Sprintf("Маркер evacuated в бакете %d отправляет поиск в новый массив. Чтение по-прежнему ничего не переносит.", oldIndex),
				Tone:   "info", Target: fmt.Sprintf("new-b%d", int(hash&uint64(len(l.buckets.Chains)-1))),
			})
		}
	}

	location, ok := l.find(key, hash)
	if ok {
		l.highlights[location.target] = true
		l.trace = append(l.trace, TraceStep{
			Phase: "read", Title: "Ключ найден",
			Detail: fmt.Sprintf("Получено значение %d. nevacuate остался равен %d.", location.entry.Value, l.nevacuate),
			Tone:   "success", Target: location.target,
		})
		return
	}
	l.trace = append(l.trace, TraceStep{Phase: "read", Title: "Ключ отсутствует", Detail: "Цепочка бакета проверена до конца; состояние map не изменилось.", Tone: "warning"})
}

func (l *Legacy) delete(key int) {
	hash := hashInt(key, l.seed)
	l.traceHash(key, hash, "удаление")
	if l.oldBuckets != nil {
		l.growWork(hash)
	}
	location, ok := l.find(key, hash)
	if !ok {
		l.trace = append(l.trace, TraceStep{Phase: "delete", Title: "Удалять нечего", Detail: "Ключ не найден.", Tone: "warning"})
		return
	}
	*location.slot = nil
	l.count--
	l.trace = append(l.trace, TraceStep{
		Phase: "delete", Title: "Слот освобождён",
		Detail: "Удаление, как и запись, выполняет growWork при активном росте. В представлении runtime tophash получает специальный empty-маркер.",
		Tone:   "success", Target: location.target,
	})
}

func (l *Legacy) startGrow() {
	oldCount := len(l.buckets.Chains)
	l.oldBuckets = l.buckets
	l.buckets = newLegacyArray(oldCount * 2)
	l.B++
	l.nevacuate = 0
	l.trace = append(l.trace, TraceStep{
		Phase: "grow", Title: fmt.Sprintf("Начался grow: %d → %d бакетов", oldCount, oldCount*2),
		Detail: "buckets уже указывает на новый массив, oldbuckets удерживает старый. Данные физически распределены между двумя массивами до завершения эвакуации.",
		Tone:   "warning", Target: "legacy-arrays",
		Formula: fmt.Sprintf("load check: count=%d > 6.5 × %d = %.1f", l.count+1, oldCount, 6.5*float64(oldCount)),
	})
}

// growWork mirrors the important order in runtime/map_noswiss.go:
//  1. evacuate the old bucket targeted by the current hash;
//  2. if growth continues, evacuate the bucket at nevacuate.
//
// These can be the same bucket. Therefore a mutation guarantees progress, but
// it does NOT guarantee two newly evacuated buckets.
func (l *Legacy) growWork(hash uint64) {
	if l.oldBuckets == nil {
		return
	}
	target := int(hash & uint64(len(l.oldBuckets.Chains)-1))
	l.trace = append(l.trace, TraceStep{
		Phase: "grow-work", Title: "Запись платит «налог на рост»",
		Detail: fmt.Sprintf("Сначала целевой старый бакет %d, затем текущий nevacuate=%d. Если это один и тот же или уже готовый бакет, новых переносов будет меньше двух.", target, l.nevacuate),
		Tone:   "warning",
	})
	l.evacuate(target, "целевой бакет операции")
	if l.oldBuckets != nil {
		l.evacuate(l.nevacuate, "плановый бакет nevacuate")
	}
}

func (l *Legacy) evacuate(oldIndex int, reason string) {
	if l.oldBuckets == nil || oldIndex < 0 || oldIndex >= len(l.oldBuckets.Chains) || l.oldBuckets.Evacuated[oldIndex] {
		l.trace = append(l.trace, TraceStep{
			Phase: "evacuate-skip", Title: "Повторный перенос не нужен",
			Detail: fmt.Sprintf("Бакет %d (%s) уже эвакуирован.", oldIndex, reason),
			Tone:   "info", Target: fmt.Sprintf("old-b%d", oldIndex),
		})
		return
	}

	oldCount := len(l.oldBuckets.Chains)
	movedX, movedY := 0, 0
	for _, bucket := range l.oldBuckets.Chains[oldIndex] {
		for _, entry := range bucket.Slots {
			if entry == nil {
				continue
			}
			newIndex := oldIndex
			if entry.Hash&uint64(oldCount) != 0 {
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
		Phase: "evacuate", Title: fmt.Sprintf("Старый бакет %d эвакуирован", oldIndex),
		Detail: fmt.Sprintf("%s: X остаётся в новом бакете %d (%d пар), Y уходит в %d (%d пар). Решает новый бит hash & oldBucketCount.", reason, oldIndex, movedX, oldIndex+oldCount, movedY),
		Tone:   "success", Target: fmt.Sprintf("old-b%d", oldIndex),
		Formula: fmt.Sprintf("newbit = %d; hash & newbit → X(0) или Y(1)", oldCount),
	})

	if oldIndex == l.nevacuate {
		for l.nevacuate < oldCount && l.oldBuckets.Evacuated[l.nevacuate] {
			l.nevacuate++
		}
	}
	if l.nevacuate == oldCount {
		l.oldBuckets = nil
		l.nevacuate = 0
		l.trace = append(l.trace, TraceStep{
			Phase: "complete", Title: "Эвакуация завершена",
			Detail: "oldbuckets обнулён. Старый массив становится недостижимым и позднее освобождается сборщиком мусора.",
			Tone:   "success", Target: "legacy-arrays",
		})
	}
}

func (l *Legacy) overLoadFactor(nextCount int) bool {
	bucketCount := 1 << l.B
	return nextCount > 8 && nextCount*2 > 13*bucketCount
}

type legacyLocation struct {
	entry  *swissEntry
	slot   **swissEntry
	target string
}

func (l *Legacy) find(key int, hash uint64) (legacyLocation, bool) {
	if l.oldBuckets != nil {
		oldIndex := int(hash & uint64(len(l.oldBuckets.Chains)-1))
		if !l.oldBuckets.Evacuated[oldIndex] {
			if location, ok := findLegacy(l.oldBuckets.Chains[oldIndex], key, "old", oldIndex); ok {
				return location, true
			}
			return legacyLocation{}, false
		}
	}
	index := int(hash & uint64(len(l.buckets.Chains)-1))
	return findLegacy(l.buckets.Chains[index], key, "new", index)
}

func findLegacy(chain []*legacyBucket, key int, generation string, bucketIndex int) (legacyLocation, bool) {
	for overflowIndex, bucket := range chain {
		for slotIndex := range bucket.Slots {
			entry := bucket.Slots[slotIndex]
			if entry != nil && entry.Key == key {
				return legacyLocation{
					entry: entry, slot: &bucket.Slots[slotIndex],
					target: legacyTarget(generation, bucketIndex, overflowIndex, slotIndex),
				}, true
			}
		}
	}
	return legacyLocation{}, false
}

func insertLegacy(chain *[]*legacyBucket, entry *swissEntry) (int, int) {
	for bucketIndex, bucket := range *chain {
		for slotIndex := range bucket.Slots {
			if bucket.Slots[slotIndex] == nil {
				bucket.Slots[slotIndex] = entry
				return bucketIndex, slotIndex
			}
		}
	}
	newBucket := &legacyBucket{}
	*chain = append(*chain, newBucket)
	newBucket.Slots[0] = entry
	return len(*chain) - 1, 0
}

func newLegacyArray(size int) *legacyArray {
	array := &legacyArray{Chains: make([][]*legacyBucket, size), Evacuated: make([]bool, size)}
	for i := range array.Chains {
		// Extra capacity makes overflow append stable for this educational
		// model. The UI scenarios never need hundreds of overflow buckets.
		array.Chains[i] = make([]*legacyBucket, 1, 32)
		array.Chains[i][0] = &legacyBucket{}
	}
	return array
}

func (l *Legacy) traceHash(key int, hash uint64, action string) {
	l.trace = append(l.trace, TraceStep{
		Phase: "hash", Title: fmt.Sprintf("Ключ %d хешируется для операции «%s»", key, action),
		Detail:  fmt.Sprintf("hash = 0x%016x. Младшие B=%d бит выбирают бакет; верхний байт служит tophash-фильтром внутри bmap.", hash, l.B),
		Formula: fmt.Sprintf("bucket = hash & (2^B − 1) = %d", int(hash&uint64((1<<l.B)-1))),
		Tone:    "info",
	})
}

func (l *Legacy) Snapshot() Snapshot {
	growing := l.oldBuckets != nil
	oldCount := 0
	progress := "—"
	if growing {
		oldCount = len(l.oldBuckets.Chains)
		progress = fmt.Sprintf("%d/%d", l.nevacuate, oldCount)
	}
	overflow := countOverflow(l.buckets)
	snapshot := Snapshot{
		Mode: "legacy", Title: "Классическая map: бакеты и эвакуация",
		Subtitle: "До Go 1.24 · hmap/bmap · tophash · overflow · oldbuckets · nevacuate",
		Notice:   "Изменяющая операция вызывает growWork: целевой старый бакет + текущий nevacuate. Это не означает два новых бакета на каждую запись.",
		Stats: []Stat{
			{Label: "count", Value: fmt.Sprint(l.count), Hint: "Число пар ключ → значение"},
			{Label: "B", Value: fmt.Sprintf("%d → %d бакетов", l.B, 1<<l.B), Hint: "Количество бакетов равно 2^B"},
			{Label: "oldbuckets", Value: map[bool]string{true: "активен", false: "nil"}[growing], Hint: "Старый массив существует только во время grow"},
			{Label: "nevacuate", Value: progress, Hint: "Все индексы меньше счётчика уже перенесены"},
			{Label: "overflow", Value: fmt.Sprint(overflow), Hint: "Дополнительные bmap в цепочках"},
		},
		Trace: l.trace, EventCount: l.events,
		Legacy: &LegacyView{
			B: l.B, NewBuckets: l.bucketViews(l.buckets, "new"),
			Nevacuate: l.nevacuate, OldBucketCount: oldCount, Growing: growing,
		},
	}
	if growing {
		snapshot.Legacy.OldBuckets = l.bucketViews(l.oldBuckets, "old")
		minimum := int(math.Ceil(float64(oldCount) / 2))
		snapshot.ScaleNote = fmt.Sprintf("Для %d старых бакетов после старта нужно от %d до %d изменяющих операций; точное число зависит от хешей целевых ключей. Чтения дают 0 прогресса.", oldCount, minimum, oldCount)
	} else {
		snapshot.ScaleNote = "Обычный рост запускается при count > 8 и count > 6.5 × 2^B. Отдельно runtime может делать same-size grow из-за избытка overflow-бакетов."
	}
	return snapshot
}

func (l *Legacy) bucketViews(array *legacyArray, generation string) []BucketView {
	if array == nil {
		return nil
	}
	views := make([]BucketView, 0, len(array.Chains))
	for bucketIndex, chain := range array.Chains {
		view := BucketView{Index: bucketIndex, Evacuated: array.Evacuated[bucketIndex]}
		for overflowIndex, bucket := range chain {
			slots := make([]SlotView, 0, 8)
			for slotIndex, entry := range bucket.Slots {
				state := "empty"
				if view.Evacuated {
					state = "evacuated"
				} else if entry != nil {
					state = "full"
				}
				slot := SlotView{
					Index: slotIndex, State: state, Control: "—",
					PhysicalID: legacyTarget(generation, bucketIndex, overflowIndex, slotIndex),
					Highlight:  l.highlights[legacyTarget(generation, bucketIndex, overflowIndex, slotIndex)],
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

func countOverflow(array *legacyArray) int {
	count := 0
	for _, chain := range array.Chains {
		if len(chain) > 1 {
			count += len(chain) - 1
		}
	}
	return count
}

func legacyTarget(generation string, bucket, overflow, slot int) string {
	return fmt.Sprintf("%s-b%d-o%d-s%d", generation, bucket, overflow, slot)
}
