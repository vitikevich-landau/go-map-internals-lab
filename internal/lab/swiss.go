package lab

import (
	"fmt"
	"sort"
)

const (
	groupSlots  = 8
	ctrlEmpty   = byte(0x80)
	ctrlDeleted = byte(0xfe)
)

type swissEntry struct {
	Key   int
	Value int
	Hash  uint64
}

type swissSlot struct {
	Control byte
	Entry   *swissEntry
}

type swissGroup struct {
	Slots [groupSlots]swissSlot
}

type swissTable struct {
	ID         int
	Used       int
	Capacity   int
	GrowthLeft int
	LocalDepth int
	Index      int
	Groups     []swissGroup
}

// Swiss models the default Go map implementation used since Go 1.24.
//
// Real Go limits a single table to 1024 slots. The laboratory lets the UI use
// a smaller limit so a split can be reached with a few clicks. The algorithm
// and 7/8 load factor remain the same.
type Swiss struct {
	seed             uint64
	used             int
	small            *swissGroup
	directory        []*swissTable
	globalDepth      int
	maxTableCapacity int
	nextTableID      int
	trace            []TraceStep
	events           int
	lastTargets      map[string]bool
}

func NewSwiss(maxTableCapacity int) *Swiss {
	if maxTableCapacity < 16 {
		maxTableCapacity = 16
	}
	// The grow algorithm relies on powers of two.
	if maxTableCapacity&(maxTableCapacity-1) != 0 {
		maxTableCapacity = 32
	}
	return &Swiss{
		seed:             0x6a09e667f3bcc909,
		maxTableCapacity: maxTableCapacity,
		nextTableID:      1,
		lastTargets:      make(map[string]bool),
		trace: []TraceStep{{
			Phase:  "ready",
			Title:  "Карта пуста",
			Detail: "Первая запись создаст одну малую группу на 8 слотов — без директории и отдельной таблицы.",
			Tone:   "info",
		}},
	}
}

func (s *Swiss) Apply(op Operation) Snapshot {
	s.trace = nil
	s.lastTargets = make(map[string]bool)
	s.events++

	switch op.Kind {
	case "read":
		s.lookup(op.Key, true)
	case "delete":
		s.delete(op.Key)
	default:
		s.insert(op.Key, op.Value)
	}
	return s.Snapshot()
}

func (s *Swiss) insert(key, value int) {
	hash := hashInt(key, s.seed)
	s.traceHash(key, hash, "запись")

	if s.small == nil && len(s.directory) == 0 {
		s.small = newSwissGroup()
		s.trace = append(s.trace, TraceStep{
			Phase: "allocate", Title: "Создана малая группа",
			Detail: "Пока элементов не больше 8, Map.dirPtr указывает прямо на одну группу. Директория ещё не нужна.",
			Tone:   "success", Target: "small-group",
		})
	}

	if s.small != nil {
		if idx := findInGroup(s.small, key, h2(hash)); idx >= 0 {
			s.small.Slots[idx].Entry.Value = value
			s.mark("small", 0, idx)
			s.trace = append(s.trace, TraceStep{
				Phase: "update", Title: "Ключ уже существовал",
				Detail: fmt.Sprintf("Слот %d найден по H2 и полной проверке ключа; значение заменено без роста.", idx),
				Tone:   "success", Target: slotTarget("small", 0, idx),
			})
			return
		}
		if s.used < groupSlots {
			idx := firstAvailable(s.small)
			s.small.Slots[idx] = swissSlot{Control: h2(hash), Entry: &swissEntry{Key: key, Value: value, Hash: hash}}
			s.used++
			s.mark("small", 0, idx)
			s.trace = append(s.trace, TraceStep{
				Phase: "insert", Title: "Запись попала в свободный слот",
				Detail: fmt.Sprintf("Control byte слота %d теперь хранит H2 = 0x%02x. В слоте лежит пара %d → %d.", idx, h2(hash), key, value),
				Tone:   "success", Target: slotTarget("small", 0, idx),
			})
			return
		}
		s.growSmallToTable()
	}

	s.insertIntoDirectory(key, value, hash)
}

func (s *Swiss) growSmallToTable() {
	oldEntries := groupEntries(s.small)
	table := s.newTable(16, 0, 0)
	for _, entry := range oldEntries {
		s.uncheckedInsert(table, entry)
	}
	s.small = nil
	s.directory = []*swissTable{table}
	s.trace = append(s.trace, TraceStep{
		Phase: "grow", Title: "Малая группа стала полноценной таблицей",
		Detail: fmt.Sprintf("Все %d старых пар синхронно перехешированы в таблицу на 16 слотов. В Go 1.24+ это происходит внутри текущей записи.", len(oldEntries)),
		Tone:   "warning", Target: tableID(table),
	})
}

func (s *Swiss) insertIntoDirectory(key, value int, hash uint64) {
	for {
		dirIndex := s.directoryIndex(hash)
		table := s.directory[dirIndex]
		s.trace = append(s.trace, TraceStep{
			Phase: "route", Title: "Директория выбрала таблицу",
			Detail: fmt.Sprintf("Верхние %d бит хеша дают индекс %d → %s.", s.globalDepth, dirIndex, tableID(table)),
			Tone:   "info", Target: fmt.Sprintf("dir-%d", dirIndex),
		})

		inserted, retry := s.putInTable(table, key, value, hash)
		if inserted {
			return
		}
		if retry {
			continue
		}
	}
}

// putInTable returns (done, retry). retry is true after a grow/split changed
// the directory, because the hash must be routed again.
func (s *Swiss) putInTable(table *swissTable, key, value int, hash uint64) (bool, bool) {
	mask := len(table.Groups) - 1
	offset := int(h1(hash)) & mask
	step := 0
	firstDeletedGroup, firstDeletedSlot := -1, -1

	for probes := 0; probes < len(table.Groups); probes++ {
		group := &table.Groups[offset]
		s.trace = append(s.trace, TraceStep{
			Phase: "probe", Title: fmt.Sprintf("Проверяется группа %d", offset),
			Detail: fmt.Sprintf("Сразу сравниваются 8 control bytes с H2 = 0x%02x. Это главный трюк Swiss Table.", h2(hash)),
			Tone:   "info", Target: groupTarget(table, offset),
		})

		for slotIndex := range group.Slots {
			slot := &group.Slots[slotIndex]
			if slot.Entry != nil && slot.Control == h2(hash) && slot.Entry.Key == key {
				slot.Entry.Value = value
				s.mark(tableID(table), offset, slotIndex)
				s.trace = append(s.trace, TraceStep{
					Phase: "update", Title: "Совпали H2 и ключ",
					Detail: fmt.Sprintf("Значение ключа %d обновлено в %s, группа %d, слот %d.", key, tableID(table), offset, slotIndex),
					Tone:   "success", Target: slotTarget(tableID(table), offset, slotIndex),
				})
				return true, false
			}
			if slot.Control == ctrlDeleted && firstDeletedGroup < 0 {
				firstDeletedGroup, firstDeletedSlot = offset, slotIndex
			}
		}

		empty := firstEmpty(group)
		if empty >= 0 {
			if table.GrowthLeft == 0 {
				if s.pruneTombstones(table) {
					s.trace = append(s.trace, TraceStep{
						Phase: "prune", Title: "Надгробия очищены",
						Detail: "Учебный rehash той же ёмкости убрал deleted-слоты. Реальный Go 1.25 сначала выполняет консервативный pruneTombstones и растёт, если очистки недостаточно.",
						Tone:   "warning", Target: tableID(table),
					})
					return false, true
				}
				s.rehash(table)
				return false, true
			}

			targetGroup, targetSlot := offset, empty
			reusedDeleted := false
			if firstDeletedGroup >= 0 {
				targetGroup, targetSlot = firstDeletedGroup, firstDeletedSlot
				reusedDeleted = true
			}
			slot := &table.Groups[targetGroup].Slots[targetSlot]
			slot.Control = h2(hash)
			slot.Entry = &swissEntry{Key: key, Value: value, Hash: hash}
			table.Used++
			s.used++
			if !reusedDeleted {
				table.GrowthLeft--
			}
			s.mark(tableID(table), targetGroup, targetSlot)
			detail := fmt.Sprintf("Пара %d → %d записана в группу %d, слот %d; growthLeft теперь %d.", key, value, targetGroup, targetSlot, table.GrowthLeft)
			if reusedDeleted {
				detail += " Переиспользован deleted-слот, поэтому growthLeft не уменьшился."
			}
			s.trace = append(s.trace, TraceStep{
				Phase: "insert", Title: "Найдено место для пары",
				Detail: detail, Tone: "success", Target: slotTarget(tableID(table), targetGroup, targetSlot),
			})
			return true, false
		}

		step++
		offset = (offset + step) & mask
	}

	s.rehash(table)
	return false, true
}

func (s *Swiss) lookup(key int, explain bool) (*swissEntry, bool) {
	hash := hashInt(key, s.seed)
	if explain {
		s.traceHash(key, hash, "чтение")
	}
	if s.small != nil {
		idx := findInGroup(s.small, key, h2(hash))
		if idx >= 0 {
			s.mark("small", 0, idx)
			if explain {
				s.trace = append(s.trace, TraceStep{Phase: "read", Title: "Ключ найден", Detail: fmt.Sprintf("В малой группе слот %d содержит %d → %d. Чтение ничего не перестраивает.", idx, key, s.small.Slots[idx].Entry.Value), Tone: "success", Target: slotTarget("small", 0, idx)})
			}
			return s.small.Slots[idx].Entry, true
		}
		if explain {
			s.trace = append(s.trace, TraceStep{Phase: "read", Title: "Ключ отсутствует", Detail: "H2 или полный ключ не совпали ни в одном занятом слоте.", Tone: "warning"})
		}
		return nil, false
	}
	if len(s.directory) == 0 {
		if explain {
			s.trace = append(s.trace, TraceStep{Phase: "read", Title: "Карта пуста", Detail: "У пустой map нет ни группы, ни директории.", Tone: "warning"})
		}
		return nil, false
	}

	table := s.directory[s.directoryIndex(hash)]
	mask := len(table.Groups) - 1
	offset, step := int(h1(hash))&mask, 0
	for probes := 0; probes < len(table.Groups); probes++ {
		group := &table.Groups[offset]
		if explain {
			s.trace = append(s.trace, TraceStep{Phase: "probe", Title: fmt.Sprintf("Группа %d: параллельная проверка H2", offset), Detail: fmt.Sprintf("Control word сравнивается с 0x%02x; затем проверяются только кандидаты.", h2(hash)), Tone: "info", Target: groupTarget(table, offset)})
		}
		for i := range group.Slots {
			slot := &group.Slots[i]
			if slot.Entry != nil && slot.Control == h2(hash) && slot.Entry.Key == key {
				s.mark(tableID(table), offset, i)
				if explain {
					s.trace = append(s.trace, TraceStep{Phase: "read", Title: "Ключ найден", Detail: fmt.Sprintf("Полное сравнение подтвердило ключ; получено значение %d. Структура map не изменилась.", slot.Entry.Value), Tone: "success", Target: slotTarget(tableID(table), offset, i)})
				}
				return slot.Entry, true
			}
		}
		if firstEmpty(group) >= 0 {
			if explain {
				s.trace = append(s.trace, TraceStep{Phase: "stop", Title: "Пустой слот завершил поиск", Detail: "После настоящего empty продолжать probe sequence бессмысленно: искомого ключа дальше быть не может.", Tone: "warning"})
			}
			return nil, false
		}
		step++
		offset = (offset + step) & mask
	}
	return nil, false
}

func (s *Swiss) delete(key int) {
	hash := hashInt(key, s.seed)
	s.traceHash(key, hash, "удаление")
	if s.small != nil {
		idx := findInGroup(s.small, key, h2(hash))
		if idx < 0 {
			s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Удалять нечего", Detail: "Ключ не найден в малой группе.", Tone: "warning"})
			return
		}
		s.small.Slots[idx] = swissSlot{Control: ctrlEmpty}
		s.used--
		s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Слот стал empty", Detail: "В малой map probe sequence нет, поэтому tombstone не нужен.", Tone: "success", Target: slotTarget("small", 0, idx)})
		return
	}
	if len(s.directory) == 0 {
		s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Карта пуста", Detail: "Операция не меняет состояние.", Tone: "warning"})
		return
	}
	table := s.directory[s.directoryIndex(hash)]
	mask := len(table.Groups) - 1
	offset, step := int(h1(hash))&mask, 0
	for probes := 0; probes < len(table.Groups); probes++ {
		group := &table.Groups[offset]
		for i := range group.Slots {
			slot := &group.Slots[i]
			if slot.Entry != nil && slot.Control == h2(hash) && slot.Entry.Key == key {
				slot.Entry = nil
				table.Used--
				s.used--
				if firstEmpty(group) >= 0 {
					slot.Control = ctrlEmpty
					table.GrowthLeft++
					s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Слот стал empty", Detail: "В группе уже был empty, значит удаление не оборвёт чужую цепочку поиска.", Tone: "success", Target: slotTarget(tableID(table), offset, i)})
				} else {
					slot.Control = ctrlDeleted
					s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Оставлен tombstone", Detail: "Группа была полной: deleted сохраняет непрерывность probe sequence для ключей, лежащих дальше.", Tone: "warning", Target: slotTarget(tableID(table), offset, i)})
				}
				return
			}
		}
		if firstEmpty(group) >= 0 {
			break
		}
		step++
		offset = (offset + step) & mask
	}
	s.trace = append(s.trace, TraceStep{Phase: "delete", Title: "Ключ не найден", Detail: "Первый empty завершил поиск; карта не изменилась.", Tone: "warning"})
}

func (s *Swiss) rehash(old *swissTable) {
	oldEntries := tableEntries(old)
	if old.Capacity*2 <= s.maxTableCapacity {
		replacement := s.newTable(old.Capacity*2, old.Index, old.LocalDepth)
		moves := make([]string, 0, len(oldEntries))
		for _, entry := range oldEntries {
			group, slot := s.uncheckedInsert(replacement, entry)
			moves = append(moves, fmt.Sprintf("%d→g%d/s%d", entry.Key, group, slot))
		}
		for i, table := range s.directory {
			if table == old {
				s.directory[i] = replacement
			}
		}
		s.trace = append(s.trace, TraceStep{
			Phase: "grow", Title: fmt.Sprintf("%s выросла %d → %d", tableID(old), old.Capacity, replacement.Capacity),
			Detail: fmt.Sprintf("Вся выбранная таблица синхронно перехеширована одной записью. Перемещено %d пар: %s.", len(oldEntries), compactMoves(moves)),
			Tone:   "warning", Target: tableID(replacement),
		})
		return
	}
	s.splitTable(old, oldEntries)
}

func (s *Swiss) splitTable(old *swissTable, entries []*swissEntry) {
	newDepth := old.LocalDepth + 1
	if old.LocalDepth == s.globalDepth {
		expanded := make([]*swissTable, 0, len(s.directory)*2)
		for _, table := range s.directory {
			expanded = append(expanded, table, table)
		}
		s.directory = expanded
		s.globalDepth++
		s.reindexTables()
		s.trace = append(s.trace, TraceStep{
			Phase: "directory", Title: "Директория удвоилась",
			Detail: fmt.Sprintf("globalDepth стал %d, поэтому теперь используются %d верхних битовых маршрутов.", s.globalDepth, len(s.directory)),
			Tone:   "warning", Target: "directory",
		})
	}

	left := s.newTable(s.maxTableCapacity, -1, newDepth)
	right := s.newTable(s.maxTableCapacity, -1, newDepth)
	for i, table := range s.directory {
		if table != old {
			continue
		}
		bit := (i >> (s.globalDepth - newDepth)) & 1
		if bit == 0 {
			s.directory[i] = left
		} else {
			s.directory[i] = right
		}
	}
	s.reindexTables()
	for _, entry := range entries {
		table := left
		mask := uint64(1) << (64 - newDepth)
		if entry.Hash&mask != 0 {
			table = right
		}
		s.uncheckedInsert(table, entry)
	}
	s.trace = append(s.trace, TraceStep{
		Phase: "split", Title: fmt.Sprintf("%s разделилась на %s и %s", tableID(old), tableID(left), tableID(right)),
		Detail: fmt.Sprintf("Перемещено %d пар. Верхний бит префикса определил левую или правую таблицу; старый объект таблицы больше не установлен в директории.", len(entries)),
		Tone:   "warning", Target: "directory",
	})
}

func (s *Swiss) pruneTombstones(table *swissTable) bool {
	tombs := tableTombstones(table)
	if tombs == 0 || tombs*10 < table.Capacity {
		return false
	}
	entries := tableEntries(table)
	replacement := s.newTable(table.Capacity, table.Index, table.LocalDepth)
	for _, entry := range entries {
		s.uncheckedInsert(replacement, entry)
	}
	for i, current := range s.directory {
		if current == table {
			s.directory[i] = replacement
		}
	}
	return true
}

func (s *Swiss) newTable(capacity, index, depth int) *swissTable {
	table := &swissTable{
		ID: s.nextTableID, Capacity: capacity, LocalDepth: depth, Index: index,
		GrowthLeft: maxGrowthLeft(capacity),
		Groups:     make([]swissGroup, capacity/groupSlots),
	}
	s.nextTableID++
	for groupIndex := range table.Groups {
		table.Groups[groupIndex] = *newSwissGroup()
	}
	return table
}

func (s *Swiss) uncheckedInsert(table *swissTable, entry *swissEntry) (int, int) {
	mask := len(table.Groups) - 1
	offset, step := int(h1(entry.Hash))&mask, 0
	for {
		group := &table.Groups[offset]
		idx := firstAvailable(group)
		if idx >= 0 {
			copyEntry := *entry
			group.Slots[idx] = swissSlot{Control: h2(entry.Hash), Entry: &copyEntry}
			table.Used++
			table.GrowthLeft--
			return offset, idx
		}
		step++
		offset = (offset + step) & mask
	}
}

func (s *Swiss) directoryIndex(hash uint64) int {
	if s.globalDepth == 0 {
		return 0
	}
	return int(hash >> (64 - s.globalDepth))
}

func (s *Swiss) reindexTables() {
	seen := make(map[*swissTable]bool)
	for i, table := range s.directory {
		if !seen[table] {
			table.Index = i
			seen[table] = true
		}
	}
}

func (s *Swiss) traceHash(key int, hash uint64, action string) {
	s.trace = append(s.trace, TraceStep{
		Phase: "hash", Title: fmt.Sprintf("Ключ %d хешируется для операции «%s»", key, action),
		Detail:  fmt.Sprintf("hash = 0x%016x; H2 = 0x%02x хранится в control byte, H1 = 0x%x задаёт начало probe sequence.", hash, h2(hash), h1(hash)),
		Formula: fmt.Sprintf("H1 = hash >> 7; H2 = hash & 0x7f = 0x%02x", h2(hash)),
		Tone:    "info",
	})
}

func (s *Swiss) mark(table string, group, slot int) {
	s.lastTargets[slotTarget(table, group, slot)] = true
}

func (s *Swiss) Snapshot() Snapshot {
	snapshot := Snapshot{
		Mode: "swiss", Title: "Современная map: Swiss Table",
		Subtitle: "Go 1.24+ · группы по 8 слотов · H1/H2 · quadratic probing · extendible hashing",
		Notice:   "Рост одной таблицы выполняется синхронно внутри вызвавшей его записи. «Инкрементальность» достигается тем, что большая map разбита на независимые таблицы.",
		Trace:    s.trace, EventCount: s.events,
		ScaleNote: fmt.Sprintf("Учебный предел таблицы: %d слотов. В реальном Go 1.25 предел равен 1024; уменьшение нужно только для быстрой демонстрации split.", s.maxTableCapacity),
	}

	if s.small != nil {
		snapshot.Stats = []Stat{
			{Label: "Элементы", Value: fmt.Sprint(s.used), Hint: "Число заполненных слотов"},
			{Label: "Режим", Value: "small map", Hint: "dirPtr указывает прямо на одну группу"},
			{Label: "Группы", Value: "1 × 8", Hint: "Директории ещё нет"},
			{Label: "Load", Value: fmt.Sprintf("%d/8", s.used), Hint: "Девятая новая пара создаст таблицу"},
		}
		snapshot.Tables = []TableView{{
			ID: "small", Used: s.used, Capacity: 8, GrowthLeft: 8 - s.used,
			Groups: []GroupView{s.groupView("small", 0, s.small)},
		}}
		return snapshot
	}

	unique := uniqueTables(s.directory)
	totalCapacity, totalGrowth := 0, 0
	for _, table := range unique {
		totalCapacity += table.Capacity
		totalGrowth += table.GrowthLeft
		snapshot.Tables = append(snapshot.Tables, s.tableView(table))
	}
	snapshot.Stats = []Stat{
		{Label: "Элементы", Value: fmt.Sprint(s.used), Hint: "Map.used по всем таблицам"},
		{Label: "Таблицы", Value: fmt.Sprint(len(unique)), Hint: "Независимо растущие Swiss tables"},
		{Label: "Ёмкость", Value: fmt.Sprint(totalCapacity), Hint: "Физические слоты всех уникальных таблиц"},
		{Label: "growthLeft", Value: fmt.Sprint(totalGrowth), Hint: "Сколько empty ещё можно занять до rehash"},
		{Label: "globalDepth", Value: fmt.Sprint(s.globalDepth), Hint: "Число верхних бит для директории"},
	}

	counts := make(map[*swissTable]int)
	for _, table := range s.directory {
		counts[table]++
	}
	for i, table := range s.directory {
		snapshot.Directory = append(snapshot.Directory, DirectoryView{
			Index: i, Bits: bitString(i, s.globalDepth), Table: tableID(table), Shared: counts[table] > 1,
		})
	}
	return snapshot
}

func (s *Swiss) tableView(table *swissTable) TableView {
	view := TableView{
		ID: tableID(table), Used: table.Used, Capacity: table.Capacity,
		GrowthLeft: table.GrowthLeft, Tombstones: tableTombstones(table),
		LocalDepth: table.LocalDepth, DirectoryAt: table.Index,
	}
	for index := range table.Groups {
		view.Groups = append(view.Groups, s.groupView(tableID(table), index, &table.Groups[index]))
	}
	return view
}

func (s *Swiss) groupView(table string, index int, group *swissGroup) GroupView {
	view := GroupView{Index: index}
	control := ""
	for slotIndex, slot := range group.Slots {
		state, label := "empty", "80"
		if slot.Control == ctrlDeleted {
			state, label = "deleted", "fe"
		} else if slot.Entry != nil {
			state, label = "full", fmt.Sprintf("%02x", slot.Control)
		}
		control += label
		slotView := SlotView{
			Index: slotIndex, State: state, Control: "0x" + label,
			Highlight:  s.lastTargets[slotTarget(table, index, slotIndex)],
			PhysicalID: slotTarget(table, index, slotIndex),
		}
		if slot.Entry != nil {
			slotView.Key = intPtr(slot.Entry.Key)
			slotView.Value = intPtr(slot.Entry.Value)
		}
		view.Slots = append(view.Slots, slotView)
	}
	view.ControlWord = "0x" + control
	return view
}

func newSwissGroup() *swissGroup {
	group := &swissGroup{}
	for i := range group.Slots {
		group.Slots[i].Control = ctrlEmpty
	}
	return group
}

func findInGroup(group *swissGroup, key int, control byte) int {
	for i, slot := range group.Slots {
		if slot.Entry != nil && slot.Control == control && slot.Entry.Key == key {
			return i
		}
	}
	return -1
}

func firstAvailable(group *swissGroup) int {
	for i, slot := range group.Slots {
		if slot.Entry == nil && (slot.Control == ctrlEmpty || slot.Control == ctrlDeleted) {
			return i
		}
	}
	return -1
}

func firstEmpty(group *swissGroup) int {
	for i, slot := range group.Slots {
		if slot.Entry == nil && slot.Control == ctrlEmpty {
			return i
		}
	}
	return -1
}

func groupEntries(group *swissGroup) []*swissEntry {
	var entries []*swissEntry
	for _, slot := range group.Slots {
		if slot.Entry != nil {
			copyEntry := *slot.Entry
			entries = append(entries, &copyEntry)
		}
	}
	return entries
}

func tableEntries(table *swissTable) []*swissEntry {
	var entries []*swissEntry
	for groupIndex := range table.Groups {
		entries = append(entries, groupEntries(&table.Groups[groupIndex])...)
	}
	return entries
}

func tableTombstones(table *swissTable) int {
	count := 0
	for groupIndex := range table.Groups {
		for _, slot := range table.Groups[groupIndex].Slots {
			if slot.Entry == nil && slot.Control == ctrlDeleted {
				count++
			}
		}
	}
	return count
}

func maxGrowthLeft(capacity int) int {
	if capacity <= groupSlots {
		return capacity
	}
	return capacity * 7 / 8
}

func uniqueTables(directory []*swissTable) []*swissTable {
	seen := make(map[*swissTable]bool)
	var result []*swissTable
	for _, table := range directory {
		if table != nil && !seen[table] {
			seen[table] = true
			result = append(result, table)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Index < result[j].Index })
	return result
}

func tableID(table *swissTable) string {
	return fmt.Sprintf("T%d", table.ID)
}

func groupTarget(table *swissTable, group int) string {
	return fmt.Sprintf("%s-g%d", tableID(table), group)
}

func slotTarget(table string, group, slot int) string {
	return fmt.Sprintf("%s-g%d-s%d", table, group, slot)
}

func compactMoves(moves []string) string {
	const limit = 12
	if len(moves) <= limit {
		return fmt.Sprint(moves)
	}
	return fmt.Sprintf("%v … и ещё %d", moves[:limit], len(moves)-limit)
}
