// Package lab contains deterministic and memory-safe models of two Go map
// implementations: the current Swiss Table and the legacy bucket map.
//
// The package deliberately does not use unsafe. Reading the real runtime layout
// is isolated in internal/inspector, so the algorithms here can be studied,
// tested and changed without depending on a particular Go release.
package lab

// Operation describes one action requested by the user.
//
// It is the common command understood by all three implementations: Swiss,
// Legacy and the live inspector. Value is ignored for read and delete.
type Operation struct {
	Kind  OperationKind `json:"kind"`
	Key   MapKey        `json:"key"`
	Value MapValue      `json:"value"`
}

// TraceStep is one small, human-readable stage of an operation.
//
// The browser displays TraceStep values as a timeline. A complicated insert can
// therefore be explained as a sequence: hash the key, choose a table, probe a
// group, compare H2 bytes, occupy a slot and possibly grow the structure.
type TraceStep struct {
	Phase   TracePhase `json:"phase"`
	Title   string     `json:"title"`
	Detail  string     `json:"detail"`
	Tone    TraceTone  `json:"tone,omitempty"`
	Target  UIObjectID `json:"target,omitempty"`
	Formula string     `json:"formula,omitempty"`
}

// Stat is one compact fact shown above the visualization.
//
// Value is a string because the same card may contain a number, an address,
// "nil", a fraction or a short status such as "active".
type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint"`
}

// SlotView is the transport representation of one physical key/value slot.
//
// Key and Value are pointers only so omitempty can distinguish an absent pair
// from a real zero key or zero value.
type SlotView struct {
	Index      SlotIndex  `json:"index"`
	State      SlotState  `json:"state"`
	Control    string     `json:"control"`
	Key        *MapKey    `json:"key,omitempty"`
	Value      *MapValue  `json:"value,omitempty"`
	Highlight  bool       `json:"highlight,omitempty"`
	PhysicalID UIObjectID `json:"physicalId,omitempty"`
}

// GroupView represents one Swiss Table group of eight slots.
//
// ControlWord concatenates the eight control bytes in their physical order,
// while Slots contains the decoded, browser-friendly representation.
type GroupView struct {
	Index       GroupIndex `json:"index"`
	ControlWord string     `json:"controlWord"`
	Slots       []SlotView `json:"slots"`
}

// TableView represents one independently growing Swiss Table.
//
// A large Go map may contain several such tables. Multiple directory positions
// can temporarily point at the same TableView when localDepth is smaller than
// the map's globalDepth.
type TableView struct {
	ID          TableID       `json:"id"`
	Used        ElementCount  `json:"used"`
	Capacity    TableCapacity `json:"capacity"`
	GrowthLeft  GrowthBudget  `json:"growthLeft"`
	Tombstones  ElementCount  `json:"tombstones"`
	LocalDepth  HashDepth     `json:"localDepth"`
	DirectoryAt DirectoryIndex `json:"directoryAt"`
	Groups      []GroupView   `json:"groups"`
}

// DirectoryView explains how one upper-bit prefix routes an operation to a
// Swiss Table.
type DirectoryView struct {
	Index  DirectoryIndex `json:"index"`
	Bits   string         `json:"bits"`
	Table  TableID        `json:"table"`
	Shared bool           `json:"shared"`
}

// BucketView is one legacy primary bucket together with its overflow chain.
//
// Chain[0] is the primary bmap; subsequent entries are overflow bmaps reached by
// following the runtime-style overflow link.
type BucketView struct {
	Index     BucketIndex  `json:"index"`
	Evacuated bool         `json:"evacuated"`
	Chain     [][]SlotView `json:"chain"`
}

// LegacyView contains the arrays that may coexist during incremental growth.
//
// NewBuckets is always present. OldBuckets exists only while evacuation is in
// progress; Nevacuate points at the next old bucket that must be processed in
// order.
type LegacyView struct {
	B              HashDepth    `json:"B"`
	NewBuckets     []BucketView `json:"newBuckets"`
	OldBuckets     []BucketView `json:"oldBuckets,omitempty"`
	Nevacuate      BucketIndex  `json:"nevacuate"`
	OldBucketCount int          `json:"oldBucketCount"`
	Growing        bool         `json:"growing"`
}

// Snapshot is the complete serializable state consumed by the browser.
//
// The same envelope is used for the safe Swiss model, safe legacy model and
// unsafe inspector. Fields irrelevant to the selected mode remain empty and are
// omitted from JSON where appropriate.
type Snapshot struct {
	Mode       SimulationMode `json:"mode"`
	Title      string         `json:"title"`
	Subtitle   string         `json:"subtitle"`
	Notice     string         `json:"notice"`
	Stats      []Stat         `json:"stats"`
	Directory  []DirectoryView `json:"directory,omitempty"`
	Tables     []TableView     `json:"tables,omitempty"`
	Legacy     *LegacyView     `json:"legacy,omitempty"`
	Trace      []TraceStep     `json:"trace"`
	EventCount EventCount      `json:"eventCount"`
	ScaleNote  string          `json:"scaleNote,omitempty"`
}

// intPtr returns a pointer to a copy of value.
//
// Snapshot fields use pointers so JSON can omit an absent key/value while still
// preserving the legitimate numeric value zero.
func intPtr(value int) *int {
	v := value
	return &v
}
