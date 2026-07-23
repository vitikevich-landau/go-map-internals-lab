// Package server connects the simulations and unsafe inspector to a small
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

//go:embed static/*
var staticFiles embed.FS

type application struct {
	mu            sync.Mutex
	swiss         *lab.Swiss
	legacy        *lab.Legacy
	real          *inspector.Lab
	swissMaxTable int
}

type request struct {
	Mode             string `json:"mode"`
	Kind             string `json:"kind"`
	Key              int    `json:"key"`
	Value            int    `json:"value"`
	Scenario         string `json:"scenario"`
	MaxTableCapacity int    `json:"maxTableCapacity"`
}

// New returns the complete local application as an http.Handler.
func New() http.Handler {
	app := &application{
		swissMaxTable: 32,
		swiss:         lab.NewSwiss(32),
		legacy:        lab.NewLegacy(),
		real:          inspector.New(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", app.state)
	mux.HandleFunc("POST /api/operation", app.operation)
	mux.HandleFunc("POST /api/reset", app.reset)
	mux.HandleFunc("POST /api/scenario", app.scenario)

	sub, _ := fs.Sub(staticFiles, "static")
	mux.Handle("/", http.FileServer(http.FS(sub)))
	return securityHeaders(mux)
}

func (a *application) state(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	writeJSON(w, http.StatusOK, a.snapshot(r.URL.Query().Get("mode")))
}

func (a *application) operation(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := decodeRequest(r, &input); err != nil {
		writeError(w, err)
		return
	}
	if input.Kind != "insert" && input.Kind != "read" && input.Kind != "delete" {
		writeError(w, errors.New("неизвестная операция"))
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	op := lab.Operation{Kind: input.Kind, Key: input.Key, Value: input.Value}
	var snapshot lab.Snapshot
	switch input.Mode {
	case "legacy":
		snapshot = a.legacy.Apply(op)
	case "real":
		snapshot = a.real.Apply(op)
	default:
		snapshot = a.swiss.Apply(op)
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *application) reset(w http.ResponseWriter, r *http.Request) {
	var input request
	if err := decodeRequest(r, &input); err != nil {
		writeError(w, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch input.Mode {
	case "legacy":
		a.legacy = lab.NewLegacy()
	case "real":
		a.real.Reset()
	default:
		if input.MaxTableCapacity != 0 {
			if input.MaxTableCapacity != 16 && input.MaxTableCapacity != 32 && input.MaxTableCapacity != 64 && input.MaxTableCapacity != 1024 {
				writeError(w, errors.New("предел таблицы должен быть 16, 32, 64 или 1024"))
				return
			}
			a.swissMaxTable = input.MaxTableCapacity
		}
		a.swiss = lab.NewSwiss(a.swissMaxTable)
	}
	writeJSON(w, http.StatusOK, a.snapshot(input.Mode))
}

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
	case "legacy":
		a.legacy = lab.NewLegacy()
		limit := 60
		if input.Scenario == "overflow" {
			limit = 120
		}
		for i := 1; i <= limit; i++ {
			snapshot = a.legacy.Apply(lab.Operation{Kind: "insert", Key: i, Value: i * 10})
			if snapshot.Legacy != nil && snapshot.Legacy.Growing && snapshot.Legacy.OldBucketCount >= 4 {
				break
			}
		}
	case "real":
		a.real.Reset()
		previousShape := ""
		for i := 1; i <= 2500; i++ {
			snapshot = a.real.Apply(lab.Operation{Kind: "insert", Key: i, Value: i * 10})
			shape := snapshotShape(snapshot)
			if i > 9 && previousShape != "" && shape != previousShape {
				break
			}
			if snapshot.Legacy != nil && snapshot.Legacy.Growing {
				break
			}
			previousShape = shape
		}
	default:
		maxTable := a.swissMaxTable
		count := 9
		switch input.Scenario {
		case "table-grow":
			count = 15
		case "split":
			maxTable = 32
			count = 29
		case "directory":
			maxTable = 16
			count = 90
		}
		a.swissMaxTable = maxTable
		a.swiss = lab.NewSwiss(maxTable)
		for i := 1; i <= count; i++ {
			snapshot = a.swiss.Apply(lab.Operation{Kind: "insert", Key: i, Value: i * 10})
		}
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (a *application) snapshot(mode string) lab.Snapshot {
	switch mode {
	case "legacy":
		return a.legacy.Snapshot()
	case "real":
		return a.real.Snapshot()
	default:
		return a.swiss.Snapshot()
	}
}

func snapshotShape(snapshot lab.Snapshot) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(len(snapshot.Directory)))
	for _, table := range snapshot.Tables {
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(table.Capacity))
	}
	return builder.String()
}

func decodeRequest(r *http.Request, value any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("некорректный JSON-запрос")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

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
