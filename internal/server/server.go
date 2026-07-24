// Package server связывает безопасные модели и unsafe-инспектор с небольшим
// локальным HTTP API, а затем раздаёт встроенный учебный интерфейс.
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

// staticFiles содержит браузерный интерфейс прямо внутри собранного бинарного
// файла. Поэтому приложение не зависит от текущей рабочей директории и наличия
// внешних HTML/CSS/JavaScript-файлов рядом с исполняемым файлом.
//
//go:embed static/*
var staticFiles embed.FS

// application владеет изменяемым состоянием всех трёх режимов лаборатории.
//
// HTTP-обработчики могут выполняться параллельно. Сами модели намеренно не
// содержат блокировок, потому что конкурентный доступ не относится к изучаемым
// алгоритмам. application.mu последовательно пропускает каждое чтение и
// изменение на границе HTTP-сервера.
type application struct {
	mu sync.Mutex

	swiss  *lab.Swiss
	legacy *lab.Legacy
	real   *inspector.Lab

	// swissMaxTable запоминает выбранный учебный предел таблицы между сбросами.
	// В настоящем runtime предел больше, но маленькое значение позволяет быстро
	// добраться до split через интерфейс.
	swissMaxTable lab.TableCapacity
}

// request — общая JSON-оболочка для запросов operation, reset и scenario.
// Каждый обработчик читает только те поля, которые относятся к его команде.
type request struct {
	Mode             lab.SimulationMode `json:"mode"`
	Kind             lab.OperationKind  `json:"kind"`
	Key              lab.MapKey         `json:"key"`
	Value            lab.MapValue       `json:"value"`
	Scenario         lab.ScenarioName   `json:"scenario"`
	MaxTableCapacity lab.TableCapacity  `json:"maxTableCapacity"`
}

// New собирает всё локальное приложение и возвращает его как http.Handler.
//
// Функция возвращает обработчик, а не запускает сетевой listener самостоятельно.
// Благодаря этому транспортные настройки остаются в main, а весь API можно
// проверять через net/http/httptest.
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

// state возвращает снимок выбранной реализации, не изменяя её состояние.
func (a *application) state(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()

	mode := lab.SimulationMode(r.URL.Query().Get("mode"))
	writeJSON(w, http.StatusOK, a.snapshot(mode))
}

// operation применяет одну команду insert, read или delete и возвращает полный
// снимок получившегося состояния.
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

// reset заменяет выбранную модель новым пустым экземпляром.
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

// scenario подготавливает детерминированное состояние, до которого вручную
// пришлось бы доходить множеством нажатий. Сценарий всё равно использует обычный
// публичный метод Apply и не изменяет внутренние поля модели в обход её API.
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

// prepareLegacyScenario добавляет пары, пока не начнётся активный и заметный в
// интерфейсе grow.
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

// prepareRealRuntimeScenario увеличивает настоящую map, пока не изменится её
// физическая форма. Точное число ключей зависит от версии runtime, поэтому
// условием остановки служит изменение структуры, а не жёстко заданный порог.
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

// prepareSwissScenario выбирает небольшой учебный предел и выполняет точное
// число записей, необходимое для демонстрации выбранного этапа роста.
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

// snapshot передаёт получение снимка выбранной модели. Пустой или неизвестный
// режим переключается на Swiss, потому что это вкладка по умолчанию в браузере.
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

// snapshotShape возвращает компактный отпечаток физической структуры настоящей
// map. Значения намеренно не учитываются: сценарию важно только событие
// grow/split.
func snapshotShape(snapshot lab.Snapshot) string {
	var builder strings.Builder
	builder.WriteString(strconv.Itoa(len(snapshot.Directory)))
	for _, table := range snapshot.Tables {
		builder.WriteByte(':')
		builder.WriteString(strconv.Itoa(table.Capacity))
	}
	return builder.String()
}

// isKnownOperation хранит проверку допустимых операций в одном месте, чтобы не
// разбрасывать сравнения строк по HTTP-обработчикам.
func isKnownOperation(kind lab.OperationKind) bool {
	switch kind {
	case lab.OperationInsert, lab.OperationRead, lab.OperationDelete:
		return true
	default:
		return false
	}
}

// isAllowedTeachingCapacity перечисляет поддерживаемые интерфейсом степени
// двойки для учебного предела таблицы.
func isAllowedTeachingCapacity(capacity lab.TableCapacity) bool {
	switch capacity {
	case 16, 32, 64, 1024:
		return true
	default:
		return false
	}
}

// decodeRequest читает один ограниченный по размеру JSON-объект и отклоняет
// неизвестные поля. Ограничение тела не позволяет выделить произвольный объём
// памяти, хотя по умолчанию сервер слушает только localhost.
func decodeRequest(r *http.Request, value any) error {
	defer r.Body.Close()

	decoder := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("некорректный JSON-запрос")
	}
	return nil
}

// writeJSON — единая точка формирования успешных ответов и ошибок в формате
// JSON.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
}

// securityHeaders добавляет консервативный набор заголовков, подходящий для
// встроенного локального интерфейса.
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
