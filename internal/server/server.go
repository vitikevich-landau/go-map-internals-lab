// Package server connects the safe simulations and unsafe inspector to a small
// local HTTP API, then serves the embedded educational interface.
package server

import (
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"go-map-internals-lab/internal/inspector"
	"go-map-internals-lab/internal/lab"
)

// staticFiles contains the browser interface at compile time, so the finished
// binary does not depend on a working directory or external asset files.
//
//go:embed static/*
var staticFiles embed.FS

// application owns the mutable state of all three laboratory modes.
//
// HTTP handlers may run concurrently. The models themselves intentionally do
// not contain locks because concurrency is not the subject of their algorithms;
// application.mu serializes every read and mutation at the server boundary.
type application struct {
	mu sync.Mutex

	swiss  *lab.Swiss
	legacy *lab.Legacy
	real   *inspector.Lab

	// swissMaxTable remembers the currently selected teaching limit across
	// resets. The real runtime limit is larger; a small value makes split easy
	// to reach from the interface.
	swissMaxTable lab.TableCapacity
}

// request is the common JSON envelope accepted by operation, reset and scenario
// endpoints. Each handler reads only the fields relevant to its command.
type request struct {
	Mode             lab.SimulationMode `json:"mode"`
	Kind             lab.OperationKind  `json:"kind"`
	Key              lab.MapKey         `json:"key"`
	Value            lab.MapValue       `json:"value"`
	Scenario         lab.ScenarioName   `json:"scenario"`
	MaxTableCapacity lab.TableCapacity  `json:"maxTableCapacity"`
}

// New assembles the complete local application as an http.Handler.
//
// Returning a handler instead of starting a listener keeps transport setup in
// main and makes the whole API testable through net/http/httptest.
func New() http.Handler {
	const defaultTeachingTableCapacity lab.TableCapacity = 32

	app := &application{
		swissMaxTable: defaultTeachingTableCapacity,
		swiss:         lab.NewSwiss(defaultTeachingTableCapacity),
		legacy:        lab.NewLegacy(),
		real:          inspector.New(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", app.state)
	mux.HandleFunc("POST /api/operation", app.operation)
	mux.HandleFunc("POST /api/reset", app.reset)
	mux.HandleFunc("POST /api/scenario", app.scenario)

	staticRoot, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	return securityHeaders(mux)
}

// state returns a read-only snapshot of the selected implementation.
func (a *application) state(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	mode := lab.SimulationMode(r.URL.Query().Get("mode"))
	writeJSON(w, http.StatusOK, a.snapshot(mode))
}

// operation applies one insert, read or delete command and returns the resulting
// complete snapshot.
func (a *application) operation(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := decodeRequest(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if !isKnownOperation(input.Kind) {
		writeError(w, errors.New("неизвестная операция"))
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	op := lab.Operation{Kind: input.Kind, Key: input.Key, Value: input.Value}
	var snapshot lab.Snapshot
	switch input.Mode {
	case lab.ModeLegacy:
		snapshot = a.legacy.Apply(op)
	case lab.ModeReal:
		snapshot = a.real.Apply(op)
	default:
		snapshot = a.swiss.Apply(op)
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// reset replaces the selected model with a fresh empty instance.
func (a *application) reset(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := decodeRequest(r, &input); err != nil {
		writeError(w, err)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	switch input.Mode {
	case lab.ModeLegacy:
		a.legacy = lab.NewLegacy()
	case lab.ModeReal:
		a.real.Reset()
	default:
		if input.MaxTableCapacity != 0 {
			if !isAllowedTeachingCapacity(input.MaxTableCapacity) {
				writeError(w, errors.New("предел таблицы должен быть 16, 32, 64 или 1024"))
				return
			}
			a.swissMaxTable = input.MaxTableCapacity
		}
		a.swiss = lab.NewSwiss(a.swissMaxTable)
	}

	writeJSON(w, http.StatusOK, a.snapshot(input.Mode))
}

// scenario prepares a deterministic state that would otherwise require many
// manual clicks. Every scenario still uses the same public Apply operation as a
// user action; it does not mutate internal fields behind the model's back.
func (a *application) scenario(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := decodeRequest(r, &input); err != nil {
		writeError(w, err)
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var snapshot lab.Snapshot
	switch input.Mode {
	case lab.ModeLegacy:
		snapshot = a.prepareLegacyScenario(input.Scenario)
	case lab.ModeReal:
		snapshot = a.prepareRealRuntimeScenario()
	default:
		snapshot = a.prepareSwissScenario(input.Scenario)
	}
	writeJSON(w, http.StatusOK, snapshot)
}

// prepareLegacyScenario inserts pairs until an active, observable grow appears.
func (a *application) prepareLegacyScenario(scenario lab.ScenarioName) lab.Snapshot {
	a.legacy = lab.NewLegacy()
	limit := 60
	if scenario == lab.ScenarioOverflow {
		limit = 120
	}

	var snapshot lab.Snapshot
	for key := 1; key <= limit; key++ {
		snapshot = a.legacy.Apply(lab.Operation{
			Kind:  lab.OperationInsert,
			Key:   key,
			Value: key * 10,
		})
		if snapshot.Legacy != nil && snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
			break
		}
	}
	return snapshot
}

// prepareRealRuntimeScenario grows a genuine map until its physical shape
// changes. The exact key count is runtime-dependent, therefore shape rather than
// a hard-coded threshold is the stopping condition.
func (a *application) prepareRealRuntimeScenario() lab.Snapshot {
	a.real.Reset()
	previousShape := ""

	var snapshot lab.Snapshot
	for key := 1; key <= 2500; key++ {
		snapshot = a.real.Apply(lab.Operation{
			Kind:  lab.OperationInsert,
			Key:   key,
			Value: key * 10,
		})
		shape := snapshotShape(snapshot)
		if key > 9 && previousShape != "" && shape != previousShape {
			break
		}
		if snapshot.Legacy != nil && snapshot.Legacy.Growing {
			break
		}
		previousShape = shape
	}
	return snapshot
}

// prepareSwissScenario selects a small teaching limit and performs the exact
// number of inserts needed to expose the requested milestone.
func (a *application) prepareSwissScenario(scenario lab.ScenarioName) lab.Snapshot {
	maxTable := a.swissMaxTable
	insertCount := 9

	switch scenario {
	case lab.ScenarioTableGrow:
		insertCount = 15
	case lab.ScenarioSplit:
		maxTable = 32
		insertCount = 29
	case lab.ScenarioDirectory:
		maxTable = 16
		insertCount = 90
	}

	a.swissMaxTable = maxTable
	a.swiss = lab.NewSwiss(maxTable)

	var snapshot lab.Snapshot
	for key := 1; key <= insertCount; key++ {
		snapshot = a.swiss.Apply(lab.Operation{
			Kind:  lab.OperationInsert,
			Key:   key,
			Value: key * 10,
		})
	}
	return snapshot
}

// snapshot delegates to the selected model. An empty or unknown mode falls back
// to Swiss because it is the default tab in the browser.
func (a *application) snapshot(mode lab.SimulationMode) lab.Snapshot {
	switch mode {
	case lab.ModeLegacy:
		return a.legacy.Snapshot()
	case lab.ModeReal:
		return a.real.Snapshot()
	default:
		return a.swiss.Snapshot()
	}
}

// snapshotShape returns a compact structural fingerprint of the live runtime
// map. Values are deliberately excluded: scenarios care only about grow/split.
func snapshotShape(snapshot lab.Snapshot) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(len(snapshot.Directory)))
	for _, table := range snapshot.Tables {
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(table.Capacity))
	}
	return builder.String()
}

// isKnownOperation centralizes validation instead of scattering string
// comparisons through handlers.
func isKnownOperation(kind lab.OperationKind) bool {
	switch kind {
	case lab.OperationInsert, lab.OperationRead, lab.OperationDelete:
		return true
	default:
		return false
	}
}

// isAllowedTeachingCapacity lists the UI-supported powers of two.
func isAllowedTeachingCapacity(capacity lab.TableCapacity) bool {
	switch capacity {
	case 16, 32, 64, 1024:
		return true
	default:
		return false
	}
}

// decodeRequest reads one bounded JSON object and rejects unknown fields.
// Limiting the body avoids allocating arbitrary amounts of memory even though
// the server listens only on localhost by default.
func decodeRequest(r *http.Request, value any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("некорректный JSON-запрос")
	}
	return nil
}

// writeJSON is the single response path for successful snapshots and errors.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// securityHeaders applies a conservative policy suitable for the embedded UI.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
