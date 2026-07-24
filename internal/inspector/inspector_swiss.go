//go:build goexperiment.swissmap

// Package inspector содержит намеренно версионно-зависимую часть лаборатории,
// которая читает внутреннее устройство настоящей map через unsafe.
//
// Этот файл компилируется при включённой Swiss map — это стандартный режим
// начиная с Go 1.24. Зеркала ниже соответствуют внутренним структурам конкретного
// runtime и не являются публичным API языка.
package inspector

import (
	"fmt"
	"runtime"
	"sort"
	"unsafe"

	"go-map-internals-lab/internal/lab"
)

const (
	realGroupSlots = 8
	realCtrlEmpty  = byte(0x80)
	realCtrlDelete = byte(0xfe)
)

// mapHeaderMirror повторяет нужные лаборатории поля internal/runtime/maps.Map.
//
// Порядок и размеры полей критичны: значение типа map фактически содержит
// указатель на такую структуру runtime. Любое расхождение с установленной версией
// Go делает чтение некорректным, поэтому этот код нельзя переносить в production.
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

// groupsMirror описывает массив физических групп одной таблицы.
//
// LengthMask равен количеству групп минус один. Runtime хранит маску вместо длины,
// потому что число групп всегда является степенью двойки.
type groupsMirror struct {
	Data       unsafe.Pointer
	LengthMask uint64
}

// tableMirror повторяет служебные поля одной независимо растущей Swiss-таблицы.
type tableMirror struct {
	Used       uint16
	Capacity   uint16
	GrowthLeft uint16
	LocalDepth uint8
	Index      int
	Groups     groupsMirror
}

// intSlotMirror повторяет один физический слот map[int]int.
//
// Фиксированные типы ключа и значения позволяют заранее знать размер и раскладку
// пары. Для универсального инспектора пришлось бы читать внутренние описатели
// типов runtime, что заметно повысило бы риск ошибки.
type intSlotMirror struct {
	Key   int
	Value int
}

// intGroupMirror повторяет группу map[int]int: одно 64-битное управляющее слово
// и восемь пар ключ/значение.
type intGroupMirror struct {
	Control uint64
	Slots   [realGroupSlots]intSlotMirror
}

// Lab владеет настоящей map[int]int и последней трассировкой её изменения.
//
// Все записи, чтения и удаления выполняются обычными операциями языка. unsafe
// используется только после завершения операции для построения снимка памяти.
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
		Title:  "Создана настоящая map[int]int",
		Detail: "Инспектор читает структуру Map установленного runtime. Все изменения выполняются обычными операциями Go.",
		Tone:   lab.TraceInfo,
	}}
	return l.Snapshot()
}

// Apply выполняет одну операцию и сравнивает физическое состояние до и после неё.
func (l *Lab) Apply(op lab.Operation) lab.Snapshot {
	before := l.capture()
	var readValue int
	var readOK bool

	switch op.Kind {
	case lab.OperationRead:
		readValue, readOK = l.data[op.Key]
	case lab.OperationDelete:
		delete(l.data, op.Key)
	default:
		l.data[op.Key] = op.Value
	}

	after := l.capture()
	l.trace = describeSwissChange(before, after, op, readValue, readOK)
	after.Trace = l.trace
	return after
}

// Snapshot возвращает текущее физическое состояние без выполнения операции.
func (l *Lab) Snapshot() lab.Snapshot {
	snapshot := l.capture()
	snapshot.Trace = l.trace
	return snapshot
}

// capture читает заголовок Map и преобразует directory, table и group в
// безопасную JSON-модель лаборатории.
func (l *Lab) capture() lab.Snapshot {
	header := *(**mapHeaderMirror)(unsafe.Pointer(&l.data))
	snapshot := lab.Snapshot{
		Mode:      lab.ModeReal,
		Title:     "Живой unsafe-инспектор",
		Subtitle:  fmt.Sprintf("%s · %s/%s · фактическая Swiss Table в памяти", runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Notice:    "В современной реализации нет hmap.B, oldbuckets и nevacuate. Инспектор показывает Map, directory, table, group и настоящие управляющие байты.",
		ScaleNote: "Внутренняя раскладка runtime не является публичным API Go. Этот режим предназначен только для обучения.",
	}
	if header == nil {
		return snapshot
	}

	snapshot.Stats = []lab.Stat{
		{Label: "Map.used", Value: fmt.Sprint(header.Used), Hint: "То же количество, что возвращает len(map)"},
		{Label: "dirLen", Value: fmt.Sprint(header.DirLen), Hint: "Ноль означает режим малой map"},
		{Label: "globalDepth", Value: fmt.Sprint(header.GlobalDepth), Hint: "Число старших бит хеша для выбора таблицы"},
		{Label: "seed", Value: fmt.Sprintf("0x%x", header.Seed), Hint: "Случайная соль хешера этой map"},
		{Label: "dirPtr", Value: fmt.Sprintf("0x%x", uintptr(header.DirPtr)), Hint: "Фактический адрес группы или директории"},
	}

	if header.DirPtr == nil {
		return snapshot
	}

	// При dirLen == 0 поле dirPtr указывает непосредственно на единственную группу,
	// а не на массив указателей таблиц.
	if header.DirLen == 0 {
		group := (*intGroupMirror)(header.DirPtr)
		snapshot.Tables = []lab.TableView{{
			ID:         lab.TableID("small"),
			Used:       lab.ElementCount(header.Used),
			Capacity:   realGroupSlots,
			GrowthLeft: lab.GrowthBudget(realGroupSlots - int(header.Used)),
			Groups:     []lab.GroupView{realGroupView("small", 0, group)},
		}}
		return snapshot
	}

	captureDirectory(header, &snapshot)
	return snapshot
}

// captureDirectory читает массив указателей таблиц, учитывая, что несколько
// элементов directory могут ссылаться на один физический объект table.
func captureDirectory(header *mapHeaderMirror, snapshot *lab.Snapshot) {
	directory := unsafe.Slice((**tableMirror)(header.DirPtr), header.DirLen)
	tableIDs := make(map[*tableMirror]lab.TableID)
	referenceCounts := make(map[*tableMirror]int)
	for _, table := range directory {
		referenceCounts[table]++
	}

	for directoryIndex, table := range directory {
		if table == nil {
			continue
		}

		id, exists := tableIDs[table]
		if !exists {
			id = lab.TableID(fmt.Sprintf("T@%x", uintptr(unsafe.Pointer(table))))
			tableIDs[table] = id
		}

		snapshot.Directory = append(snapshot.Directory, lab.DirectoryView{
			Index:  lab.DirectoryIndex(directoryIndex),
			Bits:   realBitString(directoryIndex, int(header.GlobalDepth)),
			Table:  id,
			Shared: referenceCounts[table] > 1,
		})
	}

	// Map iteration does not гарантирует порядок, поэтому уникальные таблицы
	// сортируются по их позиции Index перед построением интерфейса.
	type observedTable struct {
		table *tableMirror
		id    lab.TableID
	}
	observed := make([]observedTable, 0, len(tableIDs))
	for table, id := range tableIDs {
		observed = append(observed, observedTable{table: table, id: id})
	}
	sort.Slice(observed, func(left, right int) bool {
		return observed[left].table.Index < observed[right].table.Index
	})

	for _, item := range observed {
		snapshot.Tables = append(snapshot.Tables, realTableView(item.id, item.table))
	}
}

// realTableView преобразует одно зеркало runtime-таблицы в безопасное
// представление браузера.
func realTableView(id lab.TableID, table *tableMirror) lab.TableView {
	view := lab.TableView{
		ID:          id,
		Used:        lab.ElementCount(table.Used),
		Capacity:    lab.TableCapacity(table.Capacity),
		GrowthLeft:  lab.GrowthBudget(table.GrowthLeft),
		LocalDepth:  lab.HashDepth(table.LocalDepth),
		DirectoryAt: lab.DirectoryIndex(table.Index),
	}

	maxGrowth := int(table.Capacity) * 7 / 8
	view.Tombstones = lab.ElementCount(maxGrowth - int(table.Used) - int(table.GrowthLeft))

	groupCount := int(table.Groups.LengthMask + 1)
	for groupIndex := 0; groupIndex < groupCount; groupIndex++ {
		groupPointer := unsafe.Add(
			table.Groups.Data,
			uintptr(groupIndex)*unsafe.Sizeof(intGroupMirror{}),
		)
		view.Groups = append(
			view.Groups,
			realGroupView(id, lab.GroupIndex(groupIndex), (*intGroupMirror)(groupPointer)),
		)
	}
	return view
}

// realGroupView расшифровывает управляющее слово и восемь физических слотов.
func realGroupView(
	table lab.TableID,
	groupIndex lab.GroupIndex,
	group *intGroupMirror,
) lab.GroupView {
	view := lab.GroupView{
		Index:       groupIndex,
		ControlWord: fmt.Sprintf("0x%016x", group.Control),
	}

	for slotIndex := 0; slotIndex < realGroupSlots; slotIndex++ {
		// Управляющее слово лежит первым полем группы, поэтому каждый его байт можно
		// прочитать как смещение slotIndex от адреса структуры.
		control := *(*byte)(unsafe.Add(unsafe.Pointer(group), uintptr(slotIndex)))
		slot := lab.SlotView{
			Index:      lab.SlotIndex(slotIndex),
			Control:    fmt.Sprintf("0x%02x", control),
			PhysicalID: lab.UIObjectID(fmt.Sprintf("%s-g%d-s%d", table, groupIndex, slotIndex)),
		}

		switch {
		case control == realCtrlEmpty:
			slot.State = lab.SlotEmpty
		case control == realCtrlDelete:
			slot.State = lab.SlotDeleted
		case control&0x80 == 0:
			slot.State = lab.SlotFull
			key, value := group.Slots[slotIndex].Key, group.Slots[slotIndex].Value
			slot.Key, slot.Value = &key, &value
		default:
			slot.State = lab.SlotEmpty
		}
		view.Slots = append(view.Slots, slot)
	}
	return view
}

// describeSwissChange объясняет наблюдаемую разницу между снимками до и после
// обычной операции Go.
func describeSwissChange(
	before lab.Snapshot,
	after lab.Snapshot,
	op lab.Operation,
	readValue int,
	readOK bool,
) []lab.TraceStep {
	trace := []lab.TraceStep{{
		Phase:  lab.TraceOperation,
		Title:  fmt.Sprintf("Обычная операция Go: %s(%d)", operationLabel(op.Kind), op.Key),
		Detail: "Снимки получены непосредственно до и после операции; runtime не вызывается через поддельный API.",
		Tone:   lab.TraceInfo,
	}}

	if op.Kind == lab.OperationRead {
		detail := "Ключ отсутствует."
		if readOK {
			detail = fmt.Sprintf("Получено значение %d.", readValue)
		}
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceRead,
			Title:  "Чтение завершено",
			Detail: detail + " Структурные поля и адреса таблиц не изменились.",
			Tone:   lab.TraceSuccess,
		})
		return trace
	}

	beforePositions := positions(before)
	afterPositions := positions(after)
	moved := 0
	for key, oldPosition := range beforePositions {
		if newPosition, exists := afterPositions[key]; exists && newPosition != oldPosition {
			moved++
		}
	}

	shapeChanged := len(before.Tables) != len(after.Tables) ||
		capacities(before) != capacities(after) ||
		len(before.Directory) != len(after.Directory)
	if shapeChanged {
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceGrow,
			Title:  "Зафиксирована структурная перестройка",
			Detail: fmt.Sprintf("До: %s. После: %s. Между физическими слотами перемещено как минимум %d существующих ключей.", capacities(before), capacities(after), moved),
			Tone:   lab.TraceWarning,
			Target: "directory",
		})
	} else {
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceStable,
			Title:  "Таблица не перестраивалась",
			Detail: "Адреса и ёмкости таблиц прежние; изменилась только нужная пара или управляющий байт.",
			Tone:   lab.TraceSuccess,
		})
	}

	if position, exists := afterPositions[op.Key]; exists {
		trace = append(trace, lab.TraceStep{
			Phase:  lab.TraceLocate,
			Title:  "Физическое положение ключа",
			Detail: fmt.Sprintf("Ключ %d сейчас находится в %s.", op.Key, position),
			Tone:   lab.TraceSuccess,
			Target: position,
		})
	}
	return trace
}

// positions строит соответствие «ключ → физический слот» для сравнения снимков.
func positions(snapshot lab.Snapshot) map[lab.MapKey]lab.UIObjectID {
	result := make(map[lab.MapKey]lab.UIObjectID)
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

// capacities возвращает стабильное текстовое описание набора таблиц.
func capacities(snapshot lab.Snapshot) string {
	values := make([]string, 0, len(snapshot.Tables))
	for _, table := range snapshot.Tables {
		values = append(values, fmt.Sprintf("%s:%d", table.ID, table.Capacity))
	}
	sort.Strings(values)
	return fmt.Sprint(values)
}

// realBitString форматирует префикс directory с фиксированной шириной.
func realBitString(value, width int) string {
	if width == 0 {
		return "—"
	}
	result := make([]byte, width)
	for index := width - 1; index >= 0; index-- {
		result[index] = byte('0' + value&1)
		value >>= 1
	}
	return string(result)
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
