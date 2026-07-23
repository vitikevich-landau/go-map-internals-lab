package lab

// ─────────────────────────────────────────────────────────────────────────────
// types.go — словарь предметной области лаборатории.
//
// Алгоритмы map оперируют большим количеством обычных чисел и строк: ключами,
// хешами, индексами групп, control bytes, режимами и состояниями слотов. Без
// имён их легко перепутать, поэтому здесь каждому значению дано имя по его роли.
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
// Занятый слот хранит в нём H2; специальные значения обозначают empty и deleted.
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

// GrowthBudget — сколько новых empty-слотов таблица может занять до rehash.
type GrowthBudget = int

// ElementCount — количество живых пар ключ → значение.
type ElementCount = int

// EventCount — число операций, применённых к модели.
type EventCount = int

// HashDepth — количество верхних бит хеша, используемых директорией.
type HashDepth = int

// OperationKind — вид пользовательской операции над map.
type OperationKind = string

const (
	// OperationInsert записывает новую пару либо заменяет значение существующего ключа.
	OperationInsert OperationKind = "insert"
	// OperationRead ищет ключ, не изменяя структуру map.
	OperationRead OperationKind = "read"
	// OperationDelete удаляет ключ, если он существует.
	OperationDelete OperationKind = "delete"
)

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
	SlotEmpty     SlotState = "empty"
	SlotFull      SlotState = "full"
	SlotDeleted   SlotState = "deleted"
	SlotEvacuated SlotState = "evacuated"
)

// TracePhase — машинное имя этапа, по которому интерфейс группирует события.
type TracePhase = string

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
