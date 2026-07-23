// Package lab contains deterministic, safe simulations of both the current
// Swiss Table map and the pre-Go-1.24 bucket map.
//
// This package does not use unsafe. The separate inspector package is the only
// place that reads real runtime memory.
package lab

// Operation is one user-visible action in a simulation.
type Operation struct {
	Kind  string `json:"kind"` // insert, read, delete
	Key   int    `json:"key"`
	Value int    `json:"value"`
}

// TraceStep explains one small internal action. The UI presents these steps as
// a timeline, so even a beginner can follow a lookup or a grow.
type TraceStep struct {
	Phase   string `json:"phase"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Tone    string `json:"tone,omitempty"`    // info, success, warning, danger
	Target  string `json:"target,omitempty"`  // stable UI id of the highlighted object
	Formula string `json:"formula,omitempty"` // optional calculation shown verbatim
}

// Stat is a compact name/value/explanation tuple used in the header cards.
type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Hint  string `json:"hint"`
}

// SlotView is the common visual representation of one key/value storage slot.
type SlotView struct {
	Index      int    `json:"index"`
	State      string `json:"state"` // empty, full, deleted, evacuated
	Control    string `json:"control"`
	Key        *int   `json:"key,omitempty"`
	Value      *int   `json:"value,omitempty"`
	Highlight  bool   `json:"highlight,omitempty"`
	PhysicalID string `json:"physicalId,omitempty"`
}

// GroupView represents one modern Swiss Table group of eight slots.
type GroupView struct {
	Index       int        `json:"index"`
	ControlWord string     `json:"controlWord"`
	Slots       []SlotView `json:"slots"`
}

// TableView is one independently growing Swiss table.
type TableView struct {
	ID          string      `json:"id"`
	Used        int         `json:"used"`
	Capacity    int         `json:"capacity"`
	GrowthLeft  int         `json:"growthLeft"`
	Tombstones  int         `json:"tombstones"`
	LocalDepth  int         `json:"localDepth"`
	DirectoryAt int         `json:"directoryAt"`
	Groups      []GroupView `json:"groups"`
}

// DirectoryView shows how upper hash bits route an operation to a table.
type DirectoryView struct {
	Index  int    `json:"index"`
	Bits   string `json:"bits"`
	Table  string `json:"table"`
	Shared bool   `json:"shared"`
}

// BucketView is one legacy bucket plus its overflow chain.
type BucketView struct {
	Index     int          `json:"index"`
	Evacuated bool         `json:"evacuated"`
	Chain     [][]SlotView `json:"chain"`
}

// LegacyView contains both arrays that coexist during old-map evacuation.
type LegacyView struct {
	B              int          `json:"B"`
	NewBuckets     []BucketView `json:"newBuckets"`
	OldBuckets     []BucketView `json:"oldBuckets,omitempty"`
	Nevacuate      int          `json:"nevacuate"`
	OldBucketCount int          `json:"oldBucketCount"`
	Growing        bool         `json:"growing"`
}

// Snapshot is the full serializable state consumed by the browser.
type Snapshot struct {
	Mode       string          `json:"mode"`
	Title      string          `json:"title"`
	Subtitle   string          `json:"subtitle"`
	Notice     string          `json:"notice"`
	Stats      []Stat          `json:"stats"`
	Directory  []DirectoryView `json:"directory,omitempty"`
	Tables     []TableView     `json:"tables,omitempty"`
	Legacy     *LegacyView     `json:"legacy,omitempty"`
	Trace      []TraceStep     `json:"trace"`
	EventCount int             `json:"eventCount"`
	ScaleNote  string          `json:"scaleNote,omitempty"`
}

func intPtr(value int) *int {
	v := value
	return &v
}
