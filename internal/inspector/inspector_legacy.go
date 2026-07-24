//go:build !goexperiment.swissmap

// Package inspector содержит намеренно версионно-зависимую часть лаборатории,
// которая читает внутреннее устройство настоящей map через unsafe.
//
// Этот файл компилируется только при GOEXPERIMENT=noswissmap, когда runtime
// использует классические структуры hmap и bmap. Порядок и типы полей ниже не
// входят в гарантии совместимости Go и предназначены исключительно для обучения.
package inspector

import (
	"fmt"
	"runtime"
	"unsafe"

	"go-map-internals-lab/internal/lab"
)

const (
	// legacyBucketSlots — количество пар ключ/значение в одном bmap.
	legacyBucketSlots = 8

	// sameSizeGrowFlag отмечает рост без удвоения числа основных бакетов.
	sameSizeGrowFlag = 8

	// evacuatedX, evacuatedY и evacuatedEmpty — служебные значения tophash,
	// которыми runtime помечает уже перенесённые слоты старого бакета.
	evacuatedX     = 2
	evacuatedY     = 3
	evacuatedEmpty = 4

	// maxObservedOverflow ограничивает чтение повреждённой или неожиданно длинной
	// overflow-цепочки. Обычные сценарии лаборатории до этого предела не доходят.
	maxObservedOverflow = 32
)

// legacyHeaderMirror повторяет нужную лаборатории часть runtime.hmap.
//
// Структура является зеркалом памяти, а не самостоятельной моделью. Даже
// перестановка одного поля сделает последующее чтение некорректным для данной
// версии runtime.
type legacyHeaderMirror struct {
	Count      int
	Flags      uint8
	B          uint8
	NOverflow  uint16
	Hash0      uint32
	Buckets    unsafe.Pointer
	OldBuckets unsafe.Pointer
	Nevacuate  uintptr
	Extra      unsafe.Pointer
}

// legacyBucketMirror повторяет физическую раскладку bmap для map[int]int.
//
// Сначала расположены восемь значений tophash, затем массив ключей, массив
// значений и указатель на следующий overflow-бакет. Такая раскладка известна
// только потому, что лаборатория владеет map с фиксированными типами int/int.
type legacyBucketMirror struct {
	Tophash  [legacyBucketSlots]uint8
	Keys     [legacyBucketSlots]int
	Values   [legacyBucketSlots]int
	Overflow *legacyBucketMirror
}

// Lab владеет настоящей map[int]int и последней трассировкой её изменения.
//
// Операции выполняются обычным синтаксисом Go. unsafe используется только для
// чтения завершённого состояния до и после операции.
type Lab struct {
	data  map[int]int
	trace []lab.TraceStep
}

// New создаёт инспектор с новой пустой map.
func New() *Lab {
	result := &Lab{}
	result.Reset()
	return result
}

// Reset заменяет наблюдаемую map новым пустым экземпляром.
func (l *Lab) Reset() lab.Snapshot {
	l.data = make(map[int]int)
	l.trace = []lab.TraceStep{{
		Phase:  lab.TraceReady,
		Title:  "Настоящая legacy map создана",
		Detail: "Бинарник собран с GOEXPERIMENT=noswissmap; инспектор читает реальные hmap и bmap.",
		Tone:   lab.TraceInfo,
	}}
	return l.Snapshot()
}

// Apply выполняет одну обычную операцию Go и сравнивает снимки до и после неё.
func (l *Lab) Apply(op lab.Operation) lab.Snapshot {
	before := l.capture()
	readValue, readOK := 0, false

	switch op.Kind {
	case lab.OperationRead:
		readValue, readOK = l.data[op.Key]
	case lab.OperationDelete:
		delete(l.data, op.Key)
	default:
		l.data[op.Key] = op.Value
	}

	after := l.capture()
	l.trace = describeLegacyChange(before, after, op, readValue, readOK)
	after.Trace = l.trace
	return after
}

// Snapshot возвращает текущее физическое состояние без выполнения операции.
func (l *Lab) Snapshot() lab.Snapshot {
	snapshot := l.capture()
	snapshot.Trace = l.trace
	return snapshot
}

// capture читает заголовок hmap и преобразует доступные бакеты в безопасную
// JSON-модель лаборатории.
//
// Все разыменования unsafe сосредоточены в capture и captureLegacyBuckets. За их
// пределы наружу передаются только обычные значения и структуры пакета lab.
func (l *Lab) capture() lab.Snapshot {
	header := *(**legacyHeaderMirror)(unsafe.Pointer(&l.data))
	snapshot := lab.Snapshot{
		Mode:      lab.ModeReal,
		Title:     "Живой unsafe-инспектор",
		Subtitle:  fmt.Sprintf("%s · %s/%s · legacy map (noswissmap)", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Notice:    "Этот бинарник собран с GOEXPERIMENT=noswissmap, поэтому реальные oldbuckets и nevacuate доступны для наблюдения.",
		ScaleNote: "Внутренняя раскладка runtime не является публичным API Go. Этот режим предназначен только для обучения.",
	}
	if header == nil {
		return snapshot
	}

	growing := header.OldBuckets != nil
	snapshot.Stats = []lab.Stat{
		{Label: "hmap.count", Value: fmt.Sprint(header.Count), Hint: "Число элементов"},
		{Label: "B", Value: fmt.Sprint(header.B), Hint: "Количество основных бакетов равно 2^B"},
		{Label: "noverflow", Value: fmt.Sprint(header.NOverflow), Hint: "Приблизительное число overflow-бакетов"},
		{Label: "nevacuate", Value: fmt.Sprint(header.Nevacuate), Hint: "Следующий плановый старый бакет"},
		{Label: "oldbuckets", Value: map[bool]string{true: "не nil", false: "nil"}[growing], Hint: "Признак активной эвакуации"},
	}

	view := &lab.LegacyView{
		B:         lab.HashDepth(header.B),
		Growing:   growing,
		Nevacuate: lab.BucketIndex(header.Nevacuate),
	}

	newBucketCount := 1 << header.B
	view.NewBuckets = captureLegacyBuckets(header.Buckets, newBucketCount, "new")
	if growing {
		oldB := int(header.B) - 1
		if header.Flags&sameSizeGrowFlag != 0 {
			oldB = int(header.B)
		}
		oldBucketCount := 1 << oldB
		view.OldBucketCount = oldBucketCount
		view.OldBuckets = captureLegacyBuckets(header.OldBuckets, oldBucketCount, "old")
	}

	snapshot.Legacy = view
	return snapshot
}

// captureLegacyBuckets последовательно читает массив основных bmap и их
// overflow-цепочки.
func captureLegacyBuckets(base unsafe.Pointer, count int, generation string) []lab.BucketView {
	if base == nil {
		return nil
	}

	views := make([]lab.BucketView, 0, count)
	for bucketIndex := 0; bucketIndex < count; bucketIndex++ {
		pointer := unsafe.Add(base, uintptr(bucketIndex)*unsafe.Sizeof(legacyBucketMirror{}))
		bucket := (*legacyBucketMirror)(pointer)
		evacuated := isEvacuatedTophash(bucket.Tophash[0])
		view := lab.BucketView{Index: bucketIndex, Evacuated: evacuated}

		for overflowIndex, current := 0, bucket;
			current != nil && overflowIndex < maxObservedOverflow;
			overflowIndex, current = overflowIndex+1, current.Overflow {
			slots := make([]lab.SlotView, 0, legacyBucketSlots)
			for slotIndex := 0; slotIndex < legacyBucketSlots; slotIndex++ {
				top := current.Tophash[slotIndex]
				state := lab.SlotEmpty
				switch {
				case top >= 5:
					state = lab.SlotFull
				case isEvacuatedTophash(top):
					state = lab.SlotEvacuated
				}

				slot := lab.SlotView{
					Index:      slotIndex,
					State:      state,
					Control:    fmt.Sprintf("0x%02x", top),
					PhysicalID: lab.UIObjectID(fmt.Sprintf("%s-b%d-o%d-s%d", generation, bucketIndex, overflowIndex, slotIndex)),
				}
				if top >= 5 {
					key, value := current.Keys[slotIndex], current.Values[slotIndex]
					slot.Key, slot.Value = &key, &value
				}
				slots = append(slots, slot)
			}
			view.Chain = append(view.Chain, slots)
		}
		views = append(views, view)
	}
	return views
}

// isEvacuatedTophash проверяет служебные маркеры перенесённого legacy-слота.
func isEvacuatedTophash(top uint8) bool {
	return top == evacuatedX || top == evacuatedY || top == evacuatedEmpty
}

// describeLegacyChange объясняет наблюдаемую разницу между двумя настоящими
// снимками runtime.
func describeLegacyChange(
	before lab.Snapshot,
	after lab.Snapshot,
	op lab.Operation,
	readValue int,
	readOK bool,
) []lab.TraceStep {
	trace := []lab.TraceStep{{
		Phase:  lab.TraceOperation,
		Title:  fmt.Sprintf("Обычная операция Go: %s(%d)", operationLabel(op.Kind), op.Key),
		Detail: "Снимки памяти сделаны непосредственно до и после завершённой операции.",
		Tone:   lab.TraceInfo,
	}}

	if op.Kind == lab.OperationRead {
		detail := "Ключ отсутствует."
		if readOK {
			detail = fmt.Sprintf("Получено значение %d.", readValue)
		}
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceRead,
			Title:  "Чтение не двинуло эвакуацию",
			Detail: detail + " Значение nevacuate не изменилось.",
			Tone:   lab.TraceSuccess,
		})
		return trace
	}

	beforeGrowing := before.Legacy != nil && before.Legacy.Growing
	afterGrowing := after.Legacy != nil && after.Legacy.Growing
	switch {
	case !beforeGrowing && afterGrowing:
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceGrow,
			Title:  "Пойман старт настоящей эвакуации",
			Detail: fmt.Sprintf("Появился oldbuckets; nevacuate=%d. Часть бакетов уже могла быть перенесена этой же операцией.", after.Legacy.Nevacuate),
			Tone:   lab.TraceWarning,
		})
	case beforeGrowing && afterGrowing:
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceGrowWork,
			Title:  "growWork продвинул эвакуацию",
			Detail: fmt.Sprintf("nevacuate: %d → %d.", before.Legacy.Nevacuate, after.Legacy.Nevacuate),
			Tone:   lab.TraceWarning,
		})
	case beforeGrowing && !afterGrowing:
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceComplete,
			Title:  "Эвакуация завершена",
			Detail: "oldbuckets стал nil в этой операции.",
			Tone:   lab.TraceSuccess,
		})
	default:
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceStable,
			Title:  "Структурного роста нет",
			Detail: "Изменилась только целевая пара либо удаление не нашло ключ.",
			Tone:   lab.TraceSuccess,
		})
	}
	return trace
}

// operationLabel возвращает русское название пользовательской операции.
func operationLabel(kind lab.OperationKind) string {
	switch kind {
	case lab.OperationRead:
		return "чтение"
	case lab.OperationDelete:
		return "удаление"
	default:
		return "запись"
	}
}
