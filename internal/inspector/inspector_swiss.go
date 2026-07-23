//go:build goexperiment.swissmap

// Package inspector contains the intentionally version-sensitive unsafe part
// of the laboratory. This file is compiled only when the Swiss map experiment
// is active (the default in Go 1.24+).
package inspector

import (
	"fmt"
	"runtime"
	"sort"
	"unsafe"

	"go-map-internals-lab/internal/lab"
)

// These mirrors contain only fields needed by the laboratory. Field order and
// types follow internal/runtime/maps in Go 1.25. Never copy this technique into
// production code: none of these structures are part of Go's compatibility
// promise.
type mapHeaderMirror struct {
	Used              uint64
	Seed              uintptr
	DirPtr            unsafe.Pointer
	DirLen            int
	GlobalDepth       uint8
	GlobalShift       uint8
	Writing           uint8
	TombstonePossible bool
	ClearSeq          uint64
}

type groupsMirror struct {
	Data       unsafe.Pointer
	LengthMask uint64
}

type tableMirror struct {
	Used       uint16
	Capacity   uint16
	GrowthLeft uint16
	LocalDepth uint8
	Index      int
	Groups     groupsMirror
}

// The laboratory owns map[int]int, so its internal slot layout is known:
// control word, then eight interleaved {int key, int elem} slots.
type intSlotMirror struct {
	Key   int
	Value int
}

type intGroupMirror struct {
	Control uint64
	Slots   [8]intSlotMirror
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
	l.trace = []lab.TraceStep{{
		Phase: "ready", Title: "Создана настоящая map[int]int",
		Detail: "Unsafe-инспектор читает Map из установленного runtime. Все изменения выполняются обычными операциями языка Go.",
		Tone:   "info",
	}}
	return l.Snapshot()
}

func (l *Lab) Apply(op lab.Operation) lab.Snapshot {
	before := l.capture()
	var readValue int
	var readOK bool

	switch op.Kind {
	case "read":
		readValue, readOK = l.data[op.Key]
	case "delete":
		delete(l.data, op.Key)
	default:
		l.data[op.Key] = op.Value
	}

	after := l.capture()
	l.trace = describeSwissChange(before, after, op, readValue, readOK)
	after.Trace = l.trace
	return after
}

func (l *Lab) Snapshot() lab.Snapshot {
	snapshot := l.capture()
	snapshot.Trace = l.trace
	return snapshot
}

func (l *Lab) capture() lab.Snapshot {
	header := *(**mapHeaderMirror)(unsafe.Pointer(&l.data))
	snapshot := lab.Snapshot{
		Mode: "real", Title: "Живой unsafe-инспектор",
		Subtitle:  fmt.Sprintf("%s · %s/%s · фактическая Swiss Table в памяти", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Notice:    "На текущем Go нет hmap.B, oldbuckets и nevacuate. Инспектор показывает Map, directory, table, group и настоящие control bytes.",
		ScaleNote: "Экспериментальный код привязан к layout runtime и предназначен только для обучения. Обычные программы не должны зависеть от этих полей.",
	}
	if header == nil {
		return snapshot
	}

	snapshot.Stats = []lab.Stat{
		{Label: "Map.used", Value: fmt.Sprint(header.Used), Hint: "То же количество, что возвращает len(map)"},
		{Label: "dirLen", Value: fmt.Sprint(header.DirLen), Hint: "0 означает small-map optimization"},
		{Label: "globalDepth", Value: fmt.Sprint(header.GlobalDepth), Hint: "Верхние биты хеша для выбора таблицы"},
		{Label: "seed", Value: fmt.Sprintf("0x%x", header.Seed), Hint: "Случайная соль хешера этой map"},
		{Label: "dirPtr", Value: fmt.Sprintf("0x%x", uintptr(header.DirPtr)), Hint: "Реальный адрес группы или директории"},
	}

	if header.DirPtr == nil {
		return snapshot
	}
	if header.DirLen == 0 {
		group := (*intGroupMirror)(header.DirPtr)
		snapshot.Tables = []lab.TableView{{
			ID: "small", Used: int(header.Used), Capacity: 8, GrowthLeft: 8 - int(header.Used),
			Groups: []lab.GroupView{realGroupView("small", 0, group)},
		}}
		return snapshot
	}

	directory := unsafe.Slice((**tableMirror)(header.DirPtr), header.DirLen)
	seen := make(map[*tableMirror]string)
	counts := make(map[*tableMirror]int)
	for _, table := range directory {
		counts[table]++
	}
	for i, table := range directory {
		if table == nil {
			continue
		}
		id, ok := seen[table]
		if !ok {
			id = fmt.Sprintf("T@%x", uintptr(unsafe.Pointer(table)))
			seen[table] = id
		}
		snapshot.Directory = append(snapshot.Directory, lab.DirectoryView{
			Index: i, Bits: realBitString(i, int(header.GlobalDepth)), Table: id, Shared: counts[table] > 1,
		})
	}

	type pair struct {
		table *tableMirror
		id    string
	}
	pairs := make([]pair, 0, len(seen))
	for table, id := range seen {
		pairs = append(pairs, pair{table: table, id: id})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].table.Index < pairs[j].table.Index })
	for _, item := range pairs {
		table := item.table
		view := lab.TableView{
			ID: item.id, Used: int(table.Used), Capacity: int(table.Capacity),
			GrowthLeft: int(table.GrowthLeft), LocalDepth: int(table.LocalDepth),
			DirectoryAt: table.Index,
		}
		maxGrowth := int(table.Capacity) * 7 / 8
		view.Tombstones = maxGrowth - int(table.Used) - int(table.GrowthLeft)
		groupCount := int(table.Groups.LengthMask + 1)
		for groupIndex := 0; groupIndex < groupCount; groupIndex++ {
			groupPointer := unsafe.Add(table.Groups.Data, uintptr(groupIndex)*unsafe.Sizeof(intGroupMirror{}))
			view.Groups = append(view.Groups, realGroupView(item.id, groupIndex, (*intGroupMirror)(groupPointer)))
		}
		snapshot.Tables = append(snapshot.Tables, view)
	}
	return snapshot
}

func realGroupView(table string, groupIndex int, group *intGroupMirror) lab.GroupView {
	view := lab.GroupView{Index: groupIndex, ControlWord: fmt.Sprintf("0x%016x", group.Control)}
	for slotIndex := 0; slotIndex < 8; slotIndex++ {
		control := *(*byte)(unsafe.Add(unsafe.Pointer(group), uintptr(slotIndex)))
		slot := lab.SlotView{
			Index: slotIndex, Control: fmt.Sprintf("0x%02x", control),
			PhysicalID: fmt.Sprintf("%s-g%d-s%d", table, groupIndex, slotIndex),
		}
		switch {
		case control == 0x80:
			slot.State = "empty"
		case control == 0xfe:
			slot.State = "deleted"
		case control&0x80 == 0:
			slot.State = "full"
			key, value := group.Slots[slotIndex].Key, group.Slots[slotIndex].Value
			slot.Key, slot.Value = &key, &value
		default:
			slot.State = "empty"
		}
		view.Slots = append(view.Slots, slot)
	}
	return view
}

func describeSwissChange(before, after lab.Snapshot, op lab.Operation, readValue int, readOK bool) []lab.TraceStep {
	trace := []lab.TraceStep{{
		Phase: "operation", Title: fmt.Sprintf("Обычная операция Go: %s(%d)", op.Kind, op.Key),
		Detail: "Снимок ниже получен до и сразу после операции; runtime не вызывается через поддельный API.",
		Tone:   "info",
	}}
	if op.Kind == "read" {
		detail := "Ключ отсутствует."
		if readOK {
			detail = fmt.Sprintf("Получено значение %d.", readValue)
		}
		trace = append(trace, lab.TraceStep{Phase: "read", Title: "Чтение завершено", Detail: detail + " Структурные поля и адреса таблиц не изменились.", Tone: "success"})
		return trace
	}

	beforePositions := positions(before)
	afterPositions := positions(after)
	moved := 0
	for key, oldPosition := range beforePositions {
		if newPosition, ok := afterPositions[key]; ok && newPosition != oldPosition {
			moved++
		}
	}
	if len(before.Tables) != len(after.Tables) || capacities(before) != capacities(after) || len(before.Directory) != len(after.Directory) {
		trace = append(trace, lab.TraceStep{
			Phase: "grow", Title: "Зафиксирована структурная перестройка",
			Detail: fmt.Sprintf("До: %s. После: %s. Между физическими слотами перемещено как минимум %d существующих ключей.", capacities(before), capacities(after), moved),
			Tone:   "warning", Target: "directory",
		})
	} else {
		trace = append(trace, lab.TraceStep{
			Phase: "stable", Title: "Таблица не перестраивалась",
			Detail: "Адреса и ёмкости таблиц прежние; изменилась только нужная пара/control byte (или ключ отсутствовал при delete).",
			Tone:   "success",
		})
	}
	if position, ok := afterPositions[op.Key]; ok {
		trace = append(trace, lab.TraceStep{Phase: "locate", Title: "Физическое положение ключа", Detail: fmt.Sprintf("Ключ %d сейчас находится в %s.", op.Key, position), Tone: "success", Target: position})
	}
	return trace
}

func positions(snapshot lab.Snapshot) map[int]string {
	result := make(map[int]string)
	for _, table := range snapshot.Tables {
		for _, group := range table.Groups {
			for _, slot := range group.Slots {
				if slot.Key != nil {
					result[*slot.Key] = slot.PhysicalID
				}
			}
		}
	}
	return result
}

func capacities(snapshot lab.Snapshot) string {
	values := make([]string, 0, len(snapshot.Tables))
	for _, table := range snapshot.Tables {
		values = append(values, fmt.Sprintf("%s:%d", table.ID, table.Capacity))
	}
	sort.Strings(values)
	return fmt.Sprint(values)
}

func realBitString(value, width int) string {
	if width == 0 {
		return "—"
	}
	out := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		out[i] = byte('0' + value&1)
		value >>= 1
	}
	return string(out)
}
