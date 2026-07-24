package lab

import (
	"fmt"
	"sort"
)

const (
	// swissGroupSlots — физическое число слотов в одной группе Swiss Table.
	// Значение является частью устройства алгоритма: восемь управляющих байтов
	// проверяются вместе перед полным сравнением ключей.
	swissGroupSlots = 8

	// ctrlEmpty обозначает настоящий пустой слот. Встреча такого слота завершает
	// поиск: ключ не мог быть записан дальше по последовательности проб.
	ctrlEmpty ControlByte = 0x80

	// ctrlDeleted обозначает tombstone. Пары в слоте уже нет, но поиск обязан идти
	// дальше, потому что за ним могут находиться ключи из той же цепочки коллизий.
	ctrlDeleted ControlByte = 0xfe

	smallTableID TableID = "small"
)

// swissEntry — полная учебная запись, лежащая в занятом слоте.
//
// Hash сохраняется рядом с ключом и значением, чтобы при grow или split не
// вычислять его повторно. В настоящем runtime детали хранения зависят от типа
// ключа и внутренней реализации.
type swissEntry struct {
	Key   MapKey
	Value MapValue
	Hash  FullHash
}

// swissSlot — один физический слот группы.
//
// Control всегда содержит управляющий байт, а Entry равен nil для пустого или
// удалённого слота. Для занятого слота Control хранит H2 записи.
type swissSlot struct {
	Control ControlByte
	Entry   *swissEntry
}

// swissGroup — группа из восьми слотов, которые Swiss Table проверяет как один
// блок управляющих байтов.
type swissGroup struct {
	Slots [swissGroupSlots]swissSlot
}

// swissTable — одна независимо растущая таблица внутри большой Swiss map.
//
// Directory может содержать несколько ссылок на один и тот же объект таблицы.
// Это происходит, когда LocalDepth таблицы меньше globalDepth всей map.
type swissTable struct {
	ID         int
	Used       ElementCount
	Capacity   TableCapacity
	GrowthLeft GrowthBudget
	LocalDepth HashDepth
	Index      DirectoryIndex
	Groups     []swissGroup
}

// tablePutResult объясняет результат попытки записи в конкретную таблицу.
//
// completed означает, что пара записана или обновлена. reroute означает, что
// grow/split изменил директорию и тот же хеш нужно заново направить в таблицу.
// Структура используется вместо неочевидной пары возвращаемых bool.
type tablePutResult struct {
	completed bool
	reroute   bool
}

// Swiss моделирует реализацию map, используемую по умолчанию начиная с Go 1.24.
//
// Настоящий Go ограничивает одну таблицу 1024 слотами. Лаборатория разрешает
// выбрать меньший предел, чтобы до split можно было дойти несколькими нажатиями.
// Сам алгоритм и предельная загрузка 7/8 при этом сохраняются.
type Swiss struct {
	seed HashSeed

	// used — общее число живых пар во всей map, а не в одной таблице.
	used ElementCount

	// small используется, пока map умещается в одну группу из восьми слотов.
	// В этом режиме отдельной директории и объекта таблицы ещё нет.
	small *swissGroup

	// directory направляет операцию в одну из таблиц по старшим битам хеша.
	// Несколько элементов среза могут указывать на один *swissTable.
	directory []*swissTable

	globalDepth      HashDepth
	maxTableCapacity TableCapacity
	nextTableID      int

	trace       []TraceStep
	events      EventCount
	lastTargets map[UIObjectID]bool
}

// NewSwiss создаёт пустую детерминированную модель Swiss Table.
//
// maxTableCapacity — учебный предел одной таблицы. Алгоритм требует степень
// двойки: слишком маленькое значение заменяется на 16, а произвольное значение
// не степени двойки — на безопасный учебный предел 32.
func NewSwiss(maxTableCapacity TableCapacity) *Swiss {
	if maxTableCapacity < 16 {
		maxTableCapacity = 16
	}

	// Маски групп и удвоение ёмкости работают только для степеней двойки.
	if maxTableCapacity&(maxTableCapacity-1) != 0 {
		maxTableCapacity = 32
	}

	return &Swiss{
		seed:             0x6a09e667f3bcc909,
		maxTableCapacity: maxTableCapacity,
		nextTableID:      1,
		lastTargets:      make(map[UIObjectID]bool),
		trace: []TraceStep{{
			Phase:  TraceReady,
			Title:  "Карта пуста",
			Detail: "Первая запись создаст одну малую группу на 8 слотов — без директории и отдельной таблицы.",
			Tone:   TraceInfo,
		}},
	}
}

// Apply выполняет одну пользовательскую операцию и возвращает полный снимок.
//
// Перед каждой операцией очищается старая трассировка и набор подсветок, но сама
// структура map сохраняется. Поэтому интерфейс показывает только шаги текущего
// действия на фоне накопленного состояния.
func (s *Swiss) Apply(op Operation) Snapshot {
	s.trace = nil
	s.lastTargets = make(map[UIObjectID]bool)
	s.events++

	switch op.Kind {
	case OperationRead:
		s.read(op.Key)
	case OperationDelete:
		s.delete(op.Key)
	default:
		s.insert(op.Key, op.Value)
	}
	return s.Snapshot()
}

// insert записывает новую пару либо обновляет значение существующего ключа.
//
// Маршрут состоит из двух режимов:
//  1. пока map мала, работа идёт прямо с одной группой;
//  2. после девятой новой пары хеш сначала проходит через directory.
func (s *Swiss) insert(key MapKey, value MapValue) {
	hash := hashInt(key, s.seed)
	s.traceHash(key, hash, OperationInsert)

	s.ensureSmallGroup()
	if s.small != nil {
		if s.putInSmallGroup(key, value, hash) {
			return
		}

		// Малая группа заполнена всеми восемью парами. Перед записью девятой пары
		// существующие элементы превращаются в полноценную таблицу на 16 слотов.
		s.growSmallToTable()
	}

	s.insertIntoDirectory(key, value, hash)
}

// ensureSmallGroup лениво выделяет первую группу.
//
// Пустая map не обязана заранее хранить слоты. Группа появляется только при
// первой записи, как и small-map optimization в современном runtime.
func (s *Swiss) ensureSmallGroup() {
	if s.small != nil || len(s.directory) != 0 {
		return
	}

	s.small = newSwissGroup()
	s.trace = append(s.trace, TraceStep{
		Phase:  TraceAllocate,
		Title:  "Создана малая группа",
		Detail: "Пока элементов не больше 8, Map.dirPtr указывает прямо на одну группу. Директория ещё не нужна.",
		Tone:   TraceSuccess,
		Target: "small-group",
	})
}

// putInSmallGroup пытается полностью обработать запись в малом режиме.
//
// Возвращает true, если ключ обновлён либо пара заняла свободный слот. false
// означает, что все восемь слотов заняты и вызывающий код должен выполнить grow.
func (s *Swiss) putInSmallGroup(key MapKey, value MapValue, hash FullHash) bool {
	fingerprint := h2(hash)

	if slotIndex := findInGroup(s.small, key, fingerprint); slotIndex >= 0 {
		s.small.Slots[slotIndex].Entry.Value = value
		s.mark(smallTableID, 0, slotIndex)
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceUpdate,
			Title:  "Ключ уже существовал",
			Detail: fmt.Sprintf("Слот %d найден по H2 и полной проверке ключа; значение заменено без роста.", slotIndex),
			Tone:   TraceSuccess,
			Target: slotTarget(smallTableID, 0, slotIndex),
		})
		return true
	}

	if s.used >= swissGroupSlots {
		return false
	}

	slotIndex := firstAvailable(s.small)
	s.small.Slots[slotIndex] = swissSlot{
		Control: ControlByte(fingerprint),
		Entry:   &swissEntry{Key: key, Value: value, Hash: hash},
	}
	s.used++
	s.mark(smallTableID, 0, slotIndex)
	s.trace = append(s.trace, TraceStep{
		Phase:  TraceInsert,
		Title:  "Запись попала в свободный слот",
		Detail: fmt.Sprintf("Управляющий байт слота %d теперь хранит H2 = 0x%02x. В слоте лежит пара %d → %d.", slotIndex, fingerprint, key, value),
		Tone:   TraceSuccess,
		Target: slotTarget(smallTableID, 0, slotIndex),
	})
	return true
}

// growSmallToTable переводит map из одной малой группы в таблицу на 16 слотов.
//
// Все старые пары вставляются заново, потому что число групп изменилось и H1
// теперь выбирает физическое положение уже внутри полноценной таблицы.
func (s *Swiss) growSmallToTable() {
	oldEntries := groupEntries(s.small)
	table := s.newTable(16, 0, 0)
	for _, entry := range oldEntries {
		s.uncheckedInsert(table, entry)
	}

	s.small = nil
	s.directory = []*swissTable{table}
	s.trace = append(s.trace, TraceStep{
		Phase:  TraceGrow,
		Title:  "Малая группа стала полноценной таблицей",
		Detail: fmt.Sprintf("Все %d старых пар синхронно перехешированы в таблицу на 16 слотов. В Go 1.24+ это происходит внутри текущей записи.", len(oldEntries)),
		Tone:   TraceWarning,
		Target: tableID(table),
	})
}

// insertIntoDirectory повторяет маршрутизацию, пока запись не завершится.
//
// Повтор нужен после grow или split: директория могла начать указывать на новый
// объект таблицы, поэтому прежний выбор по хешу больше нельзя использовать.
func (s *Swiss) insertIntoDirectory(key MapKey, value MapValue, hash FullHash) {
	for {
		directoryIndex := s.directoryIndex(hash)
		table := s.directory[directoryIndex]
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceRoute,
			Title:  "Директория выбрала таблицу",
			Detail: fmt.Sprintf("Старшие %d бит хеша дают индекс %d → %s.", s.globalDepth, directoryIndex, tableID(table)),
			Tone:   TraceInfo,
			Target: UIObjectID(fmt.Sprintf("dir-%d", directoryIndex)),
		})

		result := s.putInTable(table, key, value, hash)
		if result.completed {
			return
		}
		if result.reroute {
			continue
		}
	}
}

// putInTable ищет ключ или свободный слот внутри одной выбранной таблицы.
//
// Группы посещаются по квадратичной последовательности. В каждой группе сначала
// сравниваются восемь H2, а полные ключи проверяются только у совпавших
// кандидатов. Первый tombstone запоминается как предпочтительное место записи,
// но поиск продолжается до настоящего empty, чтобы не пропустить существующий
// ключ дальше по цепочке.
func (s *Swiss) putInTable(
	table *swissTable,
	key MapKey,
	value MapValue,
	hash FullHash,
) tablePutResult {
	groupMask := len(table.Groups) - 1
	groupIndex := GroupIndex(int(h1(hash)) & groupMask)
	probeStep := 0
	firstDeletedGroup, firstDeletedSlot := GroupIndex(-1), SlotIndex(-1)
	fingerprint := h2(hash)

	for probeCount := 0; probeCount < len(table.Groups); probeCount++ {
		group := &table.Groups[groupIndex]
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceProbe,
			Title:  fmt.Sprintf("Проверяется группа %d", groupIndex),
			Detail: fmt.Sprintf("Сразу сравниваются 8 управляющих байтов с H2 = 0x%02x. Это главный приём Swiss Table.", fingerprint),
			Tone:   TraceInfo,
			Target: groupTarget(table, groupIndex),
		})

		for slotIndex := range group.Slots {
			slot := &group.Slots[slotIndex]
			if slot.Entry != nil && slot.Control == ControlByte(fingerprint) && slot.Entry.Key == key {
				slot.Entry.Value = value
				s.mark(tableID(table), groupIndex, slotIndex)
				s.trace = append(s.trace, TraceStep{
					Phase:  TraceUpdate,
					Title:  "Совпали H2 и ключ",
					Detail: fmt.Sprintf("Значение ключа %d обновлено в %s, группа %d, слот %d.", key, tableID(table), groupIndex, slotIndex),
					Tone:   TraceSuccess,
					Target: slotTarget(tableID(table), groupIndex, slotIndex),
				})
				return tablePutResult{completed: true}
			}

			if slot.Control == ctrlDeleted && firstDeletedGroup < 0 {
				firstDeletedGroup = groupIndex
				firstDeletedSlot = slotIndex
			}
		}

		emptySlot := firstEmpty(group)
		if emptySlot >= 0 {
			// GrowthLeft равен нулю, когда таблица достигла допустимой загрузки.
			// Сначала пробуем убрать накопившиеся tombstone без увеличения ёмкости.
			if table.GrowthLeft == 0 {
				if s.pruneTombstones(table) {
					s.trace = append(s.trace, TraceStep{
						Phase:  TracePrune,
						Title:  "Надгробия очищены",
						Detail: "Перехеширование той же ёмкости убрало deleted-слоты. После замены таблицы хеш нужно заново провести через директорию.",
						Tone:   TraceWarning,
						Target: tableID(table),
					})
					return tablePutResult{reroute: true}
				}

				s.rehash(table)
				return tablePutResult{reroute: true}
			}

			targetGroup, targetSlot := groupIndex, emptySlot
			reusedDeleted := false
			if firstDeletedGroup >= 0 {
				targetGroup, targetSlot = firstDeletedGroup, firstDeletedSlot
				reusedDeleted = true
			}

			slot := &table.Groups[targetGroup].Slots[targetSlot]
			slot.Control = ControlByte(fingerprint)
			slot.Entry = &swissEntry{Key: key, Value: value, Hash: hash}
			table.Used++
			s.used++
			if !reusedDeleted {
				table.GrowthLeft--
			}

			s.mark(tableID(table), targetGroup, targetSlot)
			detail := fmt.Sprintf(
				"Пара %d → %d записана в группу %d, слот %d; growthLeft теперь %d.",
				key,
				value,
				targetGroup,
				targetSlot,
				table.GrowthLeft,
			)
			if reusedDeleted {
				detail += " Переиспользован deleted-слот, поэтому growthLeft не уменьшился."
			}
			s.trace = append(s.trace, TraceStep{
				Phase:  TraceInsert,
				Title:  "Найдено место для пары",
				Detail: detail,
				Tone:   TraceSuccess,
				Target: slotTarget(tableID(table), targetGroup, targetSlot),
			})
			return tablePutResult{completed: true}
		}

		// Квадратичная последовательность посещает группы со смещениями
		// +1, +2, +3 и так далее. Маска заменяет остаток от деления, потому что
		// число групп всегда является степенью двойки.
		probeStep++
		groupIndex = GroupIndex((int(groupIndex) + probeStep) & groupMask)
	}

	// Защитная ветка: если все группы просмотрены без empty, таблицу необходимо
	// перестроить, а маршрут записи вычислить заново.
	s.rehash(table)
	return tablePutResult{reroute: true}
}

// read ищет ключ и записывает пошаговое объяснение, не изменяя map.
func (s *Swiss) read(key MapKey) {
	hash := hashInt(key, s.seed)
	s.traceHash(key, hash, OperationRead)
	fingerprint := h2(hash)

	if s.small != nil {
		slotIndex := findInGroup(s.small, key, fingerprint)
		if slotIndex >= 0 {
			s.mark(smallTableID, 0, slotIndex)
			s.trace = append(s.trace, TraceStep{
				Phase:  TraceRead,
				Title:  "Ключ найден",
				Detail: fmt.Sprintf("В малой группе слот %d содержит %d → %d. Чтение ничего не перестраивает.", slotIndex, key, s.small.Slots[slotIndex].Entry.Value),
				Tone:   TraceSuccess,
				Target: slotTarget(smallTableID, 0, slotIndex),
			})
			return
		}

		s.trace = append(s.trace, TraceStep{
			Phase:  TraceRead,
			Title:  "Ключ отсутствует",
			Detail: "H2 или полный ключ не совпали ни в одном занятом слоте.",
			Tone:   TraceWarning,
		})
		return
	}

	if len(s.directory) == 0 {
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceRead,
			Title:  "Карта пуста",
			Detail: "У пустой map нет ни группы, ни директории.",
			Tone:   TraceWarning,
		})
		return
	}

	table := s.directory[s.directoryIndex(hash)]
	groupMask := len(table.Groups) - 1
	groupIndex := GroupIndex(int(h1(hash)) & groupMask)
	probeStep := 0

	for probeCount := 0; probeCount < len(table.Groups); probeCount++ {
		group := &table.Groups[groupIndex]
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceProbe,
			Title:  fmt.Sprintf("Группа %d: параллельная проверка H2", groupIndex),
			Detail: fmt.Sprintf("Управляющее слово сравнивается с 0x%02x; затем проверяются только совпавшие кандидаты.", fingerprint),
			Tone:   TraceInfo,
			Target: groupTarget(table, groupIndex),
		})

		for slotIndex := range group.Slots {
			slot := &group.Slots[slotIndex]
			if slot.Entry != nil && slot.Control == ControlByte(fingerprint) && slot.Entry.Key == key {
				s.mark(tableID(table), groupIndex, slotIndex)
				s.trace = append(s.trace, TraceStep{
					Phase:  TraceRead,
					Title:  "Ключ найден",
					Detail: fmt.Sprintf("Полное сравнение подтвердило ключ; получено значение %d. Структура map не изменилась.", slot.Entry.Value),
					Tone:   TraceSuccess,
					Target: slotTarget(tableID(table), groupIndex, slotIndex),
				})
				return
			}
		}

		if firstEmpty(group) >= 0 {
			s.trace = append(s.trace, TraceStep{
				Phase:  TraceStop,
				Title:  "Пустой слот завершил поиск",
				Detail: "После настоящего empty продолжать последовательность проб бессмысленно: искомого ключа дальше быть не может.",
				Tone:   TraceWarning,
			})
			return
		}

		probeStep++
		groupIndex = GroupIndex((int(groupIndex) + probeStep) & groupMask)
	}

	s.trace = append(s.trace, TraceStep{
		Phase:  TraceRead,
		Title:  "Ключ отсутствует",
		Detail: "Проверены все группы таблицы; полного совпадения ключа нет.",
		Tone:   TraceWarning,
	})
}

// delete удаляет ключ и выбирает между настоящим empty и tombstone.
//
// В малом режиме цепочки проб нет, поэтому слот можно сразу сделать empty. В
// полноценной таблице удаление из полностью занятой группы обязано оставить
// deleted, иначе поиск другого ключа может ошибочно остановиться раньше времени.
func (s *Swiss) delete(key MapKey) {
	hash := hashInt(key, s.seed)
	s.traceHash(key, hash, OperationDelete)
	fingerprint := h2(hash)

	if s.small != nil {
		slotIndex := findInGroup(s.small, key, fingerprint)
		if slotIndex < 0 {
			s.trace = append(s.trace, TraceStep{
				Phase:  TraceDelete,
				Title:  "Удалять нечего",
				Detail: "Ключ не найден в малой группе.",
				Tone:   TraceWarning,
			})
			return
		}

		s.small.Slots[slotIndex] = swissSlot{Control: ctrlEmpty}
		s.used--
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceDelete,
			Title:  "Слот стал empty",
			Detail: "В малой map последовательности проб нет, поэтому tombstone не нужен.",
			Tone:   TraceSuccess,
			Target: slotTarget(smallTableID, 0, slotIndex),
		})
		return
	}

	if len(s.directory) == 0 {
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceDelete,
			Title:  "Карта пуста",
			Detail: "Операция не меняет состояние.",
			Tone:   TraceWarning,
		})
		return
	}

	table := s.directory[s.directoryIndex(hash)]
	groupMask := len(table.Groups) - 1
	groupIndex := GroupIndex(int(h1(hash)) & groupMask)
	probeStep := 0

	for probeCount := 0; probeCount < len(table.Groups); probeCount++ {
		group := &table.Groups[groupIndex]
		for slotIndex := range group.Slots {
			slot := &group.Slots[slotIndex]
			if slot.Entry == nil || slot.Control != ControlByte(fingerprint) || slot.Entry.Key != key {
				continue
			}

			slot.Entry = nil
			table.Used--
			s.used--

			if firstEmpty(group) >= 0 {
				slot.Control = ctrlEmpty
				table.GrowthLeft++
				s.trace = append(s.trace, TraceStep{
					Phase:  TraceDelete,
					Title:  "Слот стал empty",
					Detail: "В группе уже был empty, значит удаление не оборвёт чужую последовательность поиска.",
					Tone:   TraceSuccess,
					Target: slotTarget(tableID(table), groupIndex, slotIndex),
				})
			} else {
				slot.Control = ctrlDeleted
				s.trace = append(s.trace, TraceStep{
					Phase:  TraceDelete,
					Title:  "Оставлен tombstone",
					Detail: "Группа была полной: deleted сохраняет непрерывность последовательности проб для ключей, лежащих дальше.",
					Tone:   TraceWarning,
					Target: slotTarget(tableID(table), groupIndex, slotIndex),
				})
			}
			return
		}

		if firstEmpty(group) >= 0 {
			break
		}

		probeStep++
		groupIndex = GroupIndex((int(groupIndex) + probeStep) & groupMask)
	}

	s.trace = append(s.trace, TraceStep{
		Phase:  TraceDelete,
		Title:  "Ключ не найден",
		Detail: "Первый empty завершил поиск; map не изменилась.",
		Tone:   TraceWarning,
	})
}

// rehash выбирает один из двух вариантов роста конкретной таблицы.
//
// Пока удвоенная ёмкость не превышает учебный предел, таблица заменяется одной
// таблицей вдвое больше. На предельной ёмкости она разделяется на две таблицы, а
// directory начинает различать их дополнительным старшим битом хеша.
func (s *Swiss) rehash(oldTable *swissTable) {
	oldEntries := tableEntries(oldTable)
	if oldTable.Capacity*2 <= s.maxTableCapacity {
		replacement := s.newTable(
			oldTable.Capacity*2,
			oldTable.Index,
			oldTable.LocalDepth,
		)

		moves := make([]string, 0, len(oldEntries))
		for _, entry := range oldEntries {
			groupIndex, slotIndex := s.uncheckedInsert(replacement, entry)
			moves = append(moves, fmt.Sprintf("%d→g%d/s%d", entry.Key, groupIndex, slotIndex))
		}

		for directoryIndex, table := range s.directory {
			if table == oldTable {
				s.directory[directoryIndex] = replacement
			}
		}

		s.trace = append(s.trace, TraceStep{
			Phase: TraceGrow,
			Title: fmt.Sprintf(
				"%s выросла %d → %d",
				tableID(oldTable),
				oldTable.Capacity,
				replacement.Capacity,
			),
			Detail: fmt.Sprintf(
				"Вся выбранная таблица синхронно перехеширована одной записью. Перемещено %d пар: %s.",
				len(oldEntries),
				compactMoves(moves),
			),
			Tone:   TraceWarning,
			Target: tableID(replacement),
		})
		return
	}

	s.splitTable(oldTable, oldEntries)
}

// splitTable заменяет одну предельную таблицу двумя таблицами той же ёмкости.
//
// Если localDepth старой таблицы уже равен globalDepth, directory сначала
// удваивается. Затем дополнительный старший бит хеша распределяет ссылки
// директории и старые пары между левой и правой таблицами.
func (s *Swiss) splitTable(oldTable *swissTable, entries []*swissEntry) {
	newLocalDepth := oldTable.LocalDepth + 1
	if oldTable.LocalDepth == s.globalDepth {
		expandedDirectory := make([]*swissTable, 0, len(s.directory)*2)
		for _, table := range s.directory {
			expandedDirectory = append(expandedDirectory, table, table)
		}
		s.directory = expandedDirectory
		s.globalDepth++
		s.reindexTables()
		s.trace = append(s.trace, TraceStep{
			Phase:  TraceDirectory,
			Title:  "Директория удвоилась",
			Detail: fmt.Sprintf("globalDepth стал %d, поэтому теперь используются %d маршрутов по старшим битам.", s.globalDepth, len(s.directory)),
			Tone:   TraceWarning,
			Target: "directory",
		})
	}

	leftTable := s.newTable(s.maxTableCapacity, -1, newLocalDepth)
	rightTable := s.newTable(s.maxTableCapacity, -1, newLocalDepth)

	for directoryIndex, table := range s.directory {
		if table != oldTable {
			continue
		}

		splitBit := (directoryIndex >> (s.globalDepth - newLocalDepth)) & 1
		if splitBit == 0 {
			s.directory[directoryIndex] = leftTable
		} else {
			s.directory[directoryIndex] = rightTable
		}
	}
	s.reindexTables()

	// Маска выбирает новый старший бит префикса. Ноль оставляет запись слева,
	// единица направляет её в правую таблицу.
	splitMask := FullHash(1) << (64 - newLocalDepth)
	for _, entry := range entries {
		targetTable := leftTable
		if entry.Hash&splitMask != 0 {
			targetTable = rightTable
		}
		s.uncheckedInsert(targetTable, entry)
	}

	s.trace = append(s.trace, TraceStep{
		Phase: TraceSplit,
		Title: fmt.Sprintf(
			"%s разделилась на %s и %s",
			tableID(oldTable),
			tableID(leftTable),
			tableID(rightTable),
		),
		Detail: fmt.Sprintf("Перемещено %d пар. Дополнительный старший бит префикса определил левую или правую таблицу; старый объект больше не установлен в директории.", len(entries)),
		Tone:   TraceWarning,
		Target: "directory",
	})
}

// pruneTombstones очищает таблицу без увеличения её ёмкости.
//
// Такая перестройка имеет смысл только при заметном количестве deleted-слотов:
// здесь используется учебный порог 10 процентов ёмкости. Все живые пары
// вставляются в новый объект таблицы, после чего ссылки directory заменяются.
func (s *Swiss) pruneTombstones(table *swissTable) bool {
	tombstones := tableTombstones(table)
	if tombstones == 0 || tombstones*10 < table.Capacity {
		return false
	}

	entries := tableEntries(table)
	replacement := s.newTable(table.Capacity, table.Index, table.LocalDepth)
	for _, entry := range entries {
		s.uncheckedInsert(replacement, entry)
	}
	for directoryIndex, currentTable := range s.directory {
		if currentTable == table {
			s.directory[directoryIndex] = replacement
		}
	}
	return true
}

// newTable создаёт пустую таблицу заданной ёмкости и инициализирует все её
// группы управляющими байтами ctrlEmpty.
func (s *Swiss) newTable(
	capacity TableCapacity,
	directoryIndex DirectoryIndex,
	localDepth HashDepth,
) *swissTable {
	table := &swissTable{
		ID:         s.nextTableID,
		Capacity:   capacity,
		LocalDepth: localDepth,
		Index:      directoryIndex,
		GrowthLeft: maxGrowthLeft(capacity),
		Groups:     make([]swissGroup, capacity/swissGroupSlots),
	}
	s.nextTableID++

	for groupIndex := range table.Groups {
		table.Groups[groupIndex] = *newSwissGroup()
	}
	return table
}

// uncheckedInsert размещает уже проверенную уникальную запись во время
// внутренней перестройки.
//
// Функция не ищет существующий ключ и не запускает новый grow: вызывающий код
// заранее создал таблицу достаточной ёмкости и передаёт только живые уникальные
// пары. Возвращаемые индексы используются для учебного описания перемещений.
func (s *Swiss) uncheckedInsert(
	table *swissTable,
	entry *swissEntry,
) (GroupIndex, SlotIndex) {
	groupMask := len(table.Groups) - 1
	groupIndex := GroupIndex(int(h1(entry.Hash)) & groupMask)
	probeStep := 0

	for {
		group := &table.Groups[groupIndex]
		slotIndex := firstAvailable(group)
		if slotIndex >= 0 {
			entryCopy := *entry
			group.Slots[slotIndex] = swissSlot{
				Control: ControlByte(h2(entry.Hash)),
				Entry:   &entryCopy,
			}
			table.Used++
			table.GrowthLeft--
			return groupIndex, slotIndex
		}

		probeStep++
		groupIndex = GroupIndex((int(groupIndex) + probeStep) & groupMask)
	}
}

// directoryIndex извлекает globalDepth старших битов полного хеша.
//
// При globalDepth == 0 существует единственный маршрут с индексом 0.
func (s *Swiss) directoryIndex(hash FullHash) DirectoryIndex {
	if s.globalDepth == 0 {
		return 0
	}
	return DirectoryIndex(hash >> (64 - s.globalDepth))
}

// reindexTables записывает в каждую уникальную таблицу первую позицию, с которой
// она встречается в directory. Индекс используется только для стабильного
// отображения и сортировки таблиц в интерфейсе.
func (s *Swiss) reindexTables() {
	seen := make(map[*swissTable]bool)
	for directoryIndex, table := range s.directory {
		if seen[table] {
			continue
		}
		table.Index = directoryIndex
		seen[table] = true
	}
}

// traceHash добавляет в трассировку объяснение разделения полного хеша на H1 и
// H2 для выбранной операции.
func (s *Swiss) traceHash(key MapKey, hash FullHash, kind OperationKind) {
	s.trace = append(s.trace, TraceStep{
		Phase:  TraceHash,
		Title:  fmt.Sprintf("Ключ %d хешируется для операции «%s»", key, operationName(kind)),
		Detail: fmt.Sprintf("hash = 0x%016x; H2 = 0x%02x хранится в управляющем байте, H1 = 0x%x задаёт начало последовательности проб.", hash, h2(hash), h1(hash)),
		Formula: fmt.Sprintf(
			"H1 = hash >> 7; H2 = hash & 0x7f = 0x%02x",
			h2(hash),
		),
		Tone: TraceInfo,
	})
}

// mark запоминает физический слот, который нужно подсветить в следующем снимке.
func (s *Swiss) mark(table TableID, group GroupIndex, slot SlotIndex) {
	s.lastTargets[slotTarget(table, group, slot)] = true
}

// Snapshot преобразует внутреннее состояние модели в стабильную JSON-модель
// интерфейса. Внутренние указатели таблиц наружу не передаются.
func (s *Swiss) Snapshot() Snapshot {
	snapshot := Snapshot{
		Mode:       ModeSwiss,
		Title:      "Современная map: Swiss Table",
		Subtitle:   "Go 1.24+ · группы по 8 слотов · H1/H2 · quadratic probing · extendible hashing",
		Notice:     "Рост одной таблицы выполняется синхронно внутри вызвавшей его записи. «Инкрементальность» достигается тем, что большая map разбита на независимые таблицы.",
		Trace:      s.trace,
		EventCount: s.events,
		ScaleNote:  fmt.Sprintf("Учебный предел таблицы: %d слотов. В настоящем runtime предел равен 1024; уменьшение нужно только для быстрой демонстрации split.", s.maxTableCapacity),
	}

	if s.small != nil {
		snapshot.Stats = []Stat{
			{Label: "Элементы", Value: fmt.Sprint(s.used), Hint: "Число заполненных слотов"},
			{Label: "Режим", Value: "small map", Hint: "dirPtr указывает прямо на одну группу"},
			{Label: "Группы", Value: "1 × 8", Hint: "Директории ещё нет"},
			{Label: "Загрузка", Value: fmt.Sprintf("%d/8", s.used), Hint: "Девятая новая пара создаст таблицу"},
		}
		snapshot.Tables = []TableView{{
			ID:         smallTableID,
			Used:       s.used,
			Capacity:   swissGroupSlots,
			GrowthLeft: swissGroupSlots - s.used,
			Groups:     []GroupView{s.groupView(smallTableID, 0, s.small)},
		}}
		return snapshot
	}

	unique := uniqueTables(s.directory)
	totalCapacity := TableCapacity(0)
	totalGrowth := GrowthBudget(0)
	for _, table := range unique {
		totalCapacity += table.Capacity
		totalGrowth += table.GrowthLeft
		snapshot.Tables = append(snapshot.Tables, s.tableView(table))
	}

	snapshot.Stats = []Stat{
		{Label: "Элементы", Value: fmt.Sprint(s.used), Hint: "Map.used по всем таблицам"},
		{Label: "Таблицы", Value: fmt.Sprint(len(unique)), Hint: "Независимо растущие Swiss tables"},
		{Label: "Ёмкость", Value: fmt.Sprint(totalCapacity), Hint: "Физические слоты всех уникальных таблиц"},
		{Label: "growthLeft", Value: fmt.Sprint(totalGrowth), Hint: "Сколько empty ещё можно занять до перестройки"},
		{Label: "globalDepth", Value: fmt.Sprint(s.globalDepth), Hint: "Число старших бит для директории"},
	}

	referenceCounts := make(map[*swissTable]int)
	for _, table := range s.directory {
		referenceCounts[table]++
	}
	for directoryIndex, table := range s.directory {
		snapshot.Directory = append(snapshot.Directory, DirectoryView{
			Index:  directoryIndex,
			Bits:   bitString(directoryIndex, s.globalDepth),
			Table:  tableID(table),
			Shared: referenceCounts[table] > 1,
		})
	}
	return snapshot
}

// tableView преобразует одну внутреннюю таблицу в представление для браузера.
func (s *Swiss) tableView(table *swissTable) TableView {
	view := TableView{
		ID:          tableID(table),
		Used:        table.Used,
		Capacity:    table.Capacity,
		GrowthLeft:  table.GrowthLeft,
		Tombstones:  tableTombstones(table),
		LocalDepth:  table.LocalDepth,
		DirectoryAt: table.Index,
	}
	for groupIndex := range table.Groups {
		view.Groups = append(
			view.Groups,
			s.groupView(tableID(table), groupIndex, &table.Groups[groupIndex]),
		)
	}
	return view
}

// groupView расшифровывает управляющие байты и пары одной физической группы.
func (s *Swiss) groupView(
	table TableID,
	groupIndex GroupIndex,
	group *swissGroup,
) GroupView {
	view := GroupView{Index: groupIndex}
	controlWord := ""

	for slotIndex, slot := range group.Slots {
		state, label := SlotEmpty, "80"
		if slot.Control == ctrlDeleted {
			state, label = SlotDeleted, "fe"
		} else if slot.Entry != nil {
			state, label = SlotFull, fmt.Sprintf("%02x", slot.Control)
		}

		controlWord += label
		slotView := SlotView{
			Index:      slotIndex,
			State:      state,
			Control:    "0x" + label,
			Highlight:  s.lastTargets[slotTarget(table, groupIndex, slotIndex)],
			PhysicalID: slotTarget(table, groupIndex, slotIndex),
		}
		if slot.Entry != nil {
			slotView.Key = intPtr(slot.Entry.Key)
			slotView.Value = intPtr(slot.Entry.Value)
		}
		view.Slots = append(view.Slots, slotView)
	}

	view.ControlWord = "0x" + controlWord
	return view
}

// newSwissGroup создаёт группу, в которой каждый слот помечен настоящим empty.
func newSwissGroup() *swissGroup {
	group := &swissGroup{}
	for slotIndex := range group.Slots {
		group.Slots[slotIndex].Control = ctrlEmpty
	}
	return group
}

// findInGroup ищет ключ среди кандидатов с совпавшим H2.
//
// Возвращает -1, если ни один занятый слот не прошёл и быстрый фильтр H2, и
// полное сравнение ключа.
func findInGroup(
	group *swissGroup,
	key MapKey,
	fingerprint ControlFingerprint,
) SlotIndex {
	for slotIndex, slot := range group.Slots {
		if slot.Entry != nil &&
			slot.Control == ControlByte(fingerprint) &&
			slot.Entry.Key == key {
			return slotIndex
		}
	}
	return -1
}

// firstAvailable возвращает первый слот, пригодный для записи: настоящий empty
// либо tombstone. Наличие tombstone само по себе не завершает поиск ключа.
func firstAvailable(group *swissGroup) SlotIndex {
	for slotIndex, slot := range group.Slots {
		if slot.Entry == nil && (slot.Control == ctrlEmpty || slot.Control == ctrlDeleted) {
			return slotIndex
		}
	}
	return -1
}

// firstEmpty возвращает индекс первого настоящего empty. Именно такой слот
// является доказательством, что искомого ключа дальше по цепочке нет.
func firstEmpty(group *swissGroup) SlotIndex {
	for slotIndex, slot := range group.Slots {
		if slot.Entry == nil && slot.Control == ctrlEmpty {
			return slotIndex
		}
	}
	return -1
}

// groupEntries возвращает независимые копии всех живых записей группы.
func groupEntries(group *swissGroup) []*swissEntry {
	var entries []*swissEntry
	for _, slot := range group.Slots {
		if slot.Entry == nil {
			continue
		}
		entryCopy := *slot.Entry
		entries = append(entries, &entryCopy)
	}
	return entries
}

// tableEntries собирает живые записи всех групп таблицы.
func tableEntries(table *swissTable) []*swissEntry {
	var entries []*swissEntry
	for groupIndex := range table.Groups {
		entries = append(entries, groupEntries(&table.Groups[groupIndex])...)
	}
	return entries
}

// tableTombstones считает удалённые слоты, которые всё ещё занимают место в
// последовательностях проб.
func tableTombstones(table *swissTable) ElementCount {
	count := ElementCount(0)
	for groupIndex := range table.Groups {
		for _, slot := range table.Groups[groupIndex].Slots {
			if slot.Entry == nil && slot.Control == ctrlDeleted {
				count++
			}
		}
	}
	return count
}

// maxGrowthLeft вычисляет число настоящих empty, которые можно занять до
// перестройки. Для полноценной таблицы используется предельная загрузка 7/8.
func maxGrowthLeft(capacity TableCapacity) GrowthBudget {
	if capacity <= swissGroupSlots {
		return capacity
	}
	return capacity * 7 / 8
}

// uniqueTables удаляет повторяющиеся указатели из directory и сортирует таблицы
// по их первой позиции для стабильного отображения.
func uniqueTables(directory []*swissTable) []*swissTable {
	seen := make(map[*swissTable]bool)
	var result []*swissTable
	for _, table := range directory {
		if table == nil || seen[table] {
			continue
		}
		seen[table] = true
		result = append(result, table)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})
	return result
}

// tableID возвращает короткое стабильное имя таблицы для интерфейса.
func tableID(table *swissTable) TableID {
	return TableID(fmt.Sprintf("T%d", table.ID))
}

// groupTarget строит идентификатор группы для подсветки в браузере.
func groupTarget(table *swissTable, group GroupIndex) UIObjectID {
	return UIObjectID(fmt.Sprintf("%s-g%d", tableID(table), group))
}

// slotTarget строит идентификатор физического слота для подсветки в браузере.
func slotTarget(table TableID, group GroupIndex, slot SlotIndex) UIObjectID {
	return UIObjectID(fmt.Sprintf("%s-g%d-s%d", table, group, slot))
}

// compactMoves сокращает длинный список перемещений, чтобы учебная трассировка
// оставалась читаемой даже при большой таблице.
func compactMoves(moves []string) string {
	const visibleMoveLimit = 12
	if len(moves) <= visibleMoveLimit {
		return fmt.Sprint(moves)
	}
	return fmt.Sprintf(
		"%v … и ещё %d",
		moves[:visibleMoveLimit],
		len(moves)-visibleMoveLimit,
	)
}
