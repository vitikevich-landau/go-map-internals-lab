//go:build !goexperiment.swissmap

package inspector

import (
	"fmt"
	"runtime"
	"sort"
	"unsafe"

	"go-map-internals-lab/internal/lab"
)

const (
	legacyBucketSlots = 8
	sameSizeGrowFlag  = 8
	evacuatedX        = 2
	evacuatedY        = 3
	evacuatedEmpty    = 4
)

// Layout mirrors runtime.hmap and bmap for map[int]int in the legacy runtime.
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

type legacyBucketMirror struct {
	Tophash  [8]uint8
	Keys     [8]int
	Values   [8]int
	Overflow *legacyBucketMirror
}

type Lab struct {
	data  map[int]int
	trace []lab.TraceStep
}

func New() *Lab {
	result := &Lab{}
	result.Reset()
	return result
}

func (l *Lab) Reset() lab.Snapshot {
	l.data = make(map[int]int)
	l.trace = []lab.TraceStep{{Phase: "ready", Title: "Настоящая legacy map создана", Detail: "Бинарник собран с GOEXPERIMENT=noswissmap; unsafe видит hmap/bmap.", Tone: "info"}}
	return l.Snapshot()
}

func (l *Lab) Apply(op lab.Operation) lab.Snapshot {
	before := l.capture()
	value, ok := 0, false
	switch op.Kind {
	case "read":
		value, ok = l.data[op.Key]
	case "delete":
		delete(l.data, op.Key)
	default:
		l.data[op.Key] = op.Value
	}
	after := l.capture()
	l.trace = describeLegacyChange(before, after, op, value, ok)
	after.Trace = l.trace
	return after
}

func (l *Lab) Snapshot() lab.Snapshot {
	snapshot := l.capture()
	snapshot.Trace = l.trace
	return snapshot
}

func (l *Lab) capture() lab.Snapshot {
	header := *(**legacyHeaderMirror)(unsafe.Pointer(&l.data))
	snapshot := lab.Snapshot{
		Mode: "real", Title: "Живой unsafe-инспектор",
		Subtitle:  fmt.Sprintf("%s · %s/%s · legacy map (noswissmap)", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Notice:    "Этот бинарник собран с GOEXPERIMENT=noswissmap, поэтому реальные oldbuckets и nevacuate доступны для наблюдения.",
		ScaleNote: "Unsafe-layout не является публичным API Go. Используйте этот режим только как лабораторный микроскоп.",
	}
	if header == nil {
		return snapshot
	}
	growing := header.OldBuckets != nil
	snapshot.Stats = []lab.Stat{
		{Label: "hmap.count", Value: fmt.Sprint(header.Count), Hint: "Число элементов"},
		{Label: "B", Value: fmt.Sprint(header.B), Hint: "2^B основных бакетов"},
		{Label: "noverflow", Value: fmt.Sprint(header.NOverflow), Hint: "Приблизительное число overflow"},
		{Label: "nevacuate", Value: fmt.Sprint(header.Nevacuate), Hint: "Следующий плановый старый бакет"},
		{Label: "oldbuckets", Value: map[bool]string{true: "не nil", false: "nil"}[growing], Hint: "Признак активной эвакуации"},
	}
	view := &lab.LegacyView{B: int(header.B), Growing: growing, Nevacuate: int(header.Nevacuate)}
	newCount := 1 << header.B
	view.NewBuckets = captureLegacyBuckets(header.Buckets, newCount, "new")
	if growing {
		oldB := int(header.B) - 1
		if header.Flags&sameSizeGrowFlag != 0 {
			oldB = int(header.B)
		}
		oldCount := 1 << oldB
		view.OldBucketCount = oldCount
		view.OldBuckets = captureLegacyBuckets(header.OldBuckets, oldCount, "old")
	}
	snapshot.Legacy = view
	return snapshot
}

func captureLegacyBuckets(base unsafe.Pointer, count int, generation string) []lab.BucketView {
	if base == nil {
		return nil
	}
	views := make([]lab.BucketView, 0, count)
	for bucketIndex := 0; bucketIndex < count; bucketIndex++ {
		pointer := unsafe.Add(base, uintptr(bucketIndex)*unsafe.Sizeof(legacyBucketMirror{}))
		bucket := (*legacyBucketMirror)(pointer)
		evacuated := bucket.Tophash[0] == evacuatedX || bucket.Tophash[0] == evacuatedY || bucket.Tophash[0] == evacuatedEmpty
		view := lab.BucketView{Index: bucketIndex, Evacuated: evacuated}
		for overflowIndex, current := 0, bucket; current != nil && overflowIndex < 32; overflowIndex, current = overflowIndex+1, current.Overflow {
			slots := make([]lab.SlotView, 0, legacyBucketSlots)
			for slotIndex := 0; slotIndex < legacyBucketSlots; slotIndex++ {
				top := current.Tophash[slotIndex]
				state := "empty"
				if top >= 5 {
					state = "full"
				} else if top == evacuatedX || top == evacuatedY || top == evacuatedEmpty {
					state = "evacuated"
				}
				slot := lab.SlotView{
					Index: slotIndex, State: state, Control: fmt.Sprintf("0x%02x", top),
					PhysicalID: fmt.Sprintf("%s-b%d-o%d-s%d", generation, bucketIndex, overflowIndex, slotIndex),
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

func describeLegacyChange(before, after lab.Snapshot, op lab.Operation, value int, ok bool) []lab.TraceStep {
	trace := []lab.TraceStep{{Phase: "operation", Title: fmt.Sprintf("Обычная операция Go: %s(%d)", op.Kind, op.Key), Detail: "Unsafe-снимки сделаны непосредственно до и после.", Tone: "info"}}
	if op.Kind == "read" {
		detail := "Ключ отсутствует."
		if ok {
			detail = fmt.Sprintf("Получено значение %d.", value)
		}
		trace = append(trace, lab.TraceStep{Phase: "read", Title: "Чтение не двинуло эвакуацию", Detail: detail + " Сравните nevacuate до/после: он не меняется.", Tone: "success"})
		return trace
	}
	beforeGrowing := before.Legacy != nil && before.Legacy.Growing
	afterGrowing := after.Legacy != nil && after.Legacy.Growing
	switch {
	case !beforeGrowing && afterGrowing:
		trace = append(trace, lab.TraceStep{Phase: "grow", Title: "Пойман старт настоящей эвакуации", Detail: fmt.Sprintf("oldbuckets появился; nevacuate=%d. Часть бакетов уже могла быть перенесена этой же записью.", after.Legacy.Nevacuate), Tone: "warning"})
	case beforeGrowing && afterGrowing:
		trace = append(trace, lab.TraceStep{Phase: "grow-work", Title: "growWork продвинул эвакуацию", Detail: fmt.Sprintf("nevacuate: %d → %d.", before.Legacy.Nevacuate, after.Legacy.Nevacuate), Tone: "warning"})
	case beforeGrowing && !afterGrowing:
		trace = append(trace, lab.TraceStep{Phase: "complete", Title: "Эвакуация завершена", Detail: "oldbuckets стал nil в этой операции.", Tone: "success"})
	default:
		trace = append(trace, lab.TraceStep{Phase: "stable", Title: "Структурного роста нет", Detail: "Изменилась только целевая пара либо delete не нашёл ключ.", Tone: "success"})
	}
	return trace
}

// Keep sort imported in both build variants so capacities/order helpers can be
// expanded without changing the build-tag boundary.
var _ = sort.Ints
