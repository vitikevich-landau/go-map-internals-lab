package lab

// ─────────────────────────────────────────────────────────────────────────────
// types.go — словарь предметной области лаборатории.
//
// Алгоритмы map оперируют большим количеством обычных чисел и строк: ключами,
// хешами, индексами групп, управляющими байтами, режимами и состояниями слотов.
// Без смысловых имён их легко перепутать, поэтому здесь каждому значению дано
// имя по его роли в модели.
//
// Большинство объявлений ниже — псевдонимы (type X = Y). Они не создают новый
// несовместимый тип, а делают сигнатуры и структуры самодокументирующимися без
// лишних преобразований. Для учебного проекта это важнее строгой изоляции:
// существующий код и JSON-контракт остаются прежними, но читатель сразу видит,
// является ли int ключом, индексом, ёмкостью или счётчиком.
// ─────────────────────────────────────────────────────────────────────────────

// MapKey — целочисленный ключ учебной map.
type MapKey = int

// MapValue — значение, связанное с MapKey.
type MapValue = int

// HashSeed — соль, добавляемая к ключу перед вычислением хеша.
//
// В безопасных моделях соль фиксирована ради повторяемости. В настоящем runtime
// она случайна для каждого экземпляра map.
type HashSeed = uint64

// FullHash — полный 64-битный хеш ключа.
type FullHash = uint64

// ProbeHash — старшая часть FullHash (H1), выбирающая начальную группу и
// последовательность пробирования Swiss Table.
type ProbeHash = uint64

// ControlByte — служебный байт слота Swiss Table.
//
// Занятый слот хранит в нём H2; специальные значения обозначают пустой и
// удалённый слоты.
type ControlByte = byte

// ControlFingerprint — младшие семь бит хеша (H2). Они позволяют быстро
// отфильтровать неподходящие слоты до полного сравнения ключей.
type ControlFingerprint = byte

// SlotIndex — позиция слота внутри группы или legacy-бакета.
type SlotIndex = int

// GroupIndex — индекс группы из восьми слотов внутри Swiss Table.
type GroupIndex = int

// BucketIndex — индекс основного legacy-бакета.
type BucketIndex = int

// OverflowIndex — номер bmap в overflow-цепочке; ноль означает основной бакет.
type OverflowIndex = int

// DirectoryIndex — позиция в директории Swiss map.
type DirectoryIndex = int

// TableID — стабильное учебное имя таблицы, например T1 или small.
type TableID = string

// TableCapacity — физическое количество слотов в одной Swiss Table.
type TableCapacity = int

// GrowthBudget — сколько новых пустых слотов таблица может занять до rehash.
type GrowthBudget = int

// ElementCount — количество живых пар «ключ → значение».
type ElementCount = int

// EventCount — число операций, применённых к модели.
type EventCount = int

// HashDepth — количество старших бит хеша, используемых директорией.
type HashDepth = int

// OperationKind — вид пользовательской операции над map.
type OperationKind = string

const (
	// OperationInsert записывает новую пару либо заменяет значение существующего
	// ключа.
	OperationInsert OperationKind = "insert"

	// OperationRead ищет ключ, не изменяя структуру map.
	OperationRead OperationKind = "read"

	// OperationDelete удаляет ключ, если он существует.
	OperationDelete OperationKind = "delete"
)

// operationName возвращает русское название операции для учебной трассировки.
// Машинное значение OperationKind при этом остаётся неизменным в JSON.
func operationName(kind OperationKind) string {
	switch kind {
	case OperationRead:
		return "чтение"
	case OperationDelete:
		return "удаление"
	default:
		return "запись"
	}
}

// SimulationMode — реализация map, выбранная в интерфейсе лаборатории.
type SimulationMode = string

const (
	// ModeSwiss запускает безопасную модель современной Swiss Table.
	ModeSwiss SimulationMode = "swiss"

	// ModeLegacy запускает безопасную модель map до Go 1.24.
	ModeLegacy SimulationMode = "legacy"

	// ModeReal запускает unsafe-инспектор настоящей map текущего runtime.
	ModeReal SimulationMode = "real"
)

// SlotState — визуальное состояние физического слота.
type SlotState = string

const (
	// SlotEmpty обозначает слот, который никогда не занимали либо который можно
	// считать настоящей границей последовательности поиска.
	SlotEmpty SlotState = "empty"

	// SlotFull обозначает занятый слот с живой парой ключ/значение.
	SlotFull SlotState = "full"

	// SlotDeleted обозначает tombstone: пара удалена, но поиск обязан пройти дальше.
	SlotDeleted SlotState = "deleted"

	// SlotEvacuated обозначает старый legacy-слот после переноса в новый массив.
	SlotEvacuated SlotState = "evacuated"
)

// TracePhase — машинное имя этапа, по которому интерфейс группирует события.
type TracePhase = string

const (
	TraceReady        TracePhase = "ready"
	TraceOperation    TracePhase = "operation"
	TraceHash         TracePhase = "hash"
	TraceAllocate     TracePhase = "allocate"
	TraceRoute        TracePhase = "route"
	TraceProbe        TracePhase = "probe"
	TraceInsert       TracePhase = "insert"
	TraceUpdate       TracePhase = "update"
	TraceRead         TracePhase = "read"
	TraceDelete       TracePhase = "delete"
	TraceStop         TracePhase = "stop"
	TraceGrow         TracePhase = "grow"
	TraceGrowWork     TracePhase = "grow-work"
	TracePrune        TracePhase = "prune"
	TraceDirectory    TracePhase = "directory"
	TraceSplit        TracePhase = "split"
	TraceEvacuate     TracePhase = "evacuate"
	TraceEvacuateSkip TracePhase = "evacuate-skip"
	TraceComplete     TracePhase = "complete"
	TraceStable       TracePhase = "stable"
	TraceLocate       TracePhase = "locate"
)

// TraceTone — визуальный акцент шага трассировки.
type TraceTone = string

const (
	TraceInfo    TraceTone = "info"
	TraceSuccess TraceTone = "success"
	TraceWarning TraceTone = "warning"
	TraceDanger  TraceTone = "danger"
)

// UIObjectID — стабильный идентификатор группы, слота, таблицы или директории,
// которую браузер должен подсветить при показе шага трассировки.
type UIObjectID = string

// ScenarioName — имя заранее подготовленного учебного сценария.
type ScenarioName = string

const (
	ScenarioOverflow  ScenarioName = "overflow"
	ScenarioTableGrow ScenarioName = "table-grow"
	ScenarioSplit     ScenarioName = "split"
	ScenarioDirectory ScenarioName = "directory"
)
