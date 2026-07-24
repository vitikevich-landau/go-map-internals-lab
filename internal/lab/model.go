// Package lab содержит детерминированные и безопасные для памяти модели двух
// реализаций map в Go: современной Swiss Table и классической бакетной map.
//
// Пакет намеренно не использует unsafe. Чтение настоящей раскладки runtime
// изолировано в internal/inspector, поэтому алгоритмы здесь можно изучать,
// проверять и изменять без привязки к конкретному выпуску Go.
package lab

// Operation описывает одну операцию, которую запросил пользователь.
//
// Это общая команда для всех трёх реализаций: Swiss, Legacy и живого инспектора.
// Для чтения и удаления поле Value не используется.
type Operation struct {
	Kind  OperationKind `json:"kind"`
	Key   MapKey        `json:"key"`
	Value MapValue      `json:"value"`
}

// TraceStep — один небольшой человекочитаемый этап выполнения операции.
//
// Браузер показывает эти значения как временную шкалу. Поэтому сложную запись
// можно разложить на последовательность: вычислить хеш, выбрать таблицу,
// проверить группу, сравнить H2, занять слот и при необходимости вырастить
// структуру.
type TraceStep struct {
	Phase   TracePhase `json:"phase"`
	Title   string     `json:"title"`
	Detail  string     `json:"detail"`
	Tone    TraceTone  `json:"tone,omitempty"`
	Target  UIObjectID `json:"target,omitempty"`
	Formula string     `json:"formula,omitempty"`
}

// Stat — один компактный факт, который показывается над визуализацией.
//
// Value хранится строкой, потому что одна и та же карточка может содержать число,
// адрес, nil, дробь или короткое состояние вроде «активен».
type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint"`
}

// SlotView — транспортное представление одного физического слота пары
// «ключ → значение».
//
// Key и Value являются указателями только ради omitempty: так JSON отличает
// отсутствующую пару от настоящего нулевого ключа или нулевого значения.
type SlotView struct {
	Index      SlotIndex  `json:"index"`
	State      SlotState  `json:"state"`
	Control    string     `json:"control"`
	Key        *MapKey    `json:"key,omitempty"`
	Value      *MapValue  `json:"value,omitempty"`
	Highlight  bool       `json:"highlight,omitempty"`
	PhysicalID UIObjectID `json:"physicalId,omitempty"`
}

// GroupView представляет одну группу Swiss Table из восьми слотов.
//
// ControlWord объединяет восемь управляющих байтов в их физическом порядке, а
// Slots содержит уже расшифрованное представление, удобное для браузера.
type GroupView struct {
	Index       GroupIndex `json:"index"`
	ControlWord string     `json:"controlWord"`
	Slots       []SlotView `json:"slots"`
}

// TableView представляет одну независимо растущую Swiss Table.
//
// Большая map может содержать несколько таких таблиц. Несколько позиций
// директории могут временно указывать на одну TableView, если localDepth меньше
// globalDepth всей map.
type TableView struct {
	ID          TableID        `json:"id"`
	Used        ElementCount   `json:"used"`
	Capacity    TableCapacity  `json:"capacity"`
	GrowthLeft  GrowthBudget   `json:"growthLeft"`
	Tombstones  ElementCount   `json:"tombstones"`
	LocalDepth  HashDepth      `json:"localDepth"`
	DirectoryAt DirectoryIndex `json:"directoryAt"`
	Groups      []GroupView    `json:"groups"`
}

// DirectoryView показывает, как один префикс из старших битов хеша направляет
// операцию в конкретную Swiss Table.
type DirectoryView struct {
	Index  DirectoryIndex `json:"index"`
	Bits   string         `json:"bits"`
	Table  TableID        `json:"table"`
	Shared bool           `json:"shared"`
}

// BucketView представляет основной legacy-бакет вместе с его цепочкой
// переполнения.
//
// Chain[0] — основной bmap. Последующие элементы — дополнительные bmap,
// достижимые по ссылкам, похожим на ссылки внутри runtime.
type BucketView struct {
	Index     BucketIndex  `json:"index"`
	Evacuated bool         `json:"evacuated"`
	Chain     [][]SlotView `json:"chain"`
}

// LegacyView содержит массивы бакетов, которые могут одновременно существовать
// во время инкрементального роста.
//
// NewBuckets присутствует всегда. OldBuckets существует только во время
// эвакуации, а Nevacuate указывает на следующий старый бакет, который нужно
// последовательно обработать.
type LegacyView struct {
	B              HashDepth    `json:"B"`
	NewBuckets     []BucketView `json:"newBuckets"`
	OldBuckets     []BucketView `json:"oldBuckets,omitempty"`
	Nevacuate      BucketIndex  `json:"nevacuate"`
	OldBucketCount int          `json:"oldBucketCount"`
	Growing        bool         `json:"growing"`
}

// Snapshot — полное сериализуемое состояние, которое получает браузер.
//
// Одна и та же оболочка используется для безопасной Swiss-модели, безопасной
// legacy-модели и unsafe-инспектора. Поля, не относящиеся к выбранному режиму,
// остаются пустыми и при необходимости исключаются из JSON.
type Snapshot struct {
	Mode       SimulationMode  `json:"mode"`
	Title      string          `json:"title"`
	Subtitle   string          `json:"subtitle"`
	Notice     string          `json:"notice"`
	Stats      []Stat          `json:"stats"`
	Directory  []DirectoryView `json:"directory,omitempty"`
	Tables     []TableView     `json:"tables,omitempty"`
	Legacy     *LegacyView     `json:"legacy,omitempty"`
	Trace      []TraceStep     `json:"trace"`
	EventCount EventCount      `json:"eventCount"`
	ScaleNote  string          `json:"scaleNote,omitempty"`
}

// intPtr возвращает указатель на копию value.
//
// Поля Snapshot используют указатели, чтобы JSON мог пропустить отсутствующий
// ключ или значение, но при этом сохранить допустимое числовое значение 0.
func intPtr(value int) *int {
	v := value
	return &v
}
