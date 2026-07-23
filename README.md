# Go Map Lab

Интерактивная локальная лаборатория, которая показывает внутреннее устройство `map` в Go — от первого хеша до роста таблицы и перемещения данных.

Проект специально разделяет три уровня:

1. **Современная Go 1.24+ map (Swiss Table)** — детерминированная пошаговая модель с группами, control bytes, H1/H2, probing, tombstones, grow, split и directory.
2. **Классическая map до Go 1.24** — модель `hmap/bmap`, overflow-цепочек, `oldbuckets`, `nevacuate` и инкрементальной эвакуации.
3. **Живой unsafe-инспектор** — читает настоящие структуры `map[int]int` из установленного runtime и сравнивает память до/после обычной операции Go.

Весь интерфейс и сервер работают на стандартной библиотеке. Node.js, npm, база данных и интернет для запуска не нужны.

## Самый быстрый запуск

Откройте PowerShell в папке проекта:

```powershell
go run .
```

Затем откройте:

```text
http://127.0.0.1:8080
```

Или запустите готовый помощник:

```powershell
.\run.ps1
```

Другой порт:

```powershell
.\run.ps1 -Port 9090
```

## Что нажимать в интерфейсе

### 1. Swiss Table

- **9-я запись: small → table** показывает переход от одной малой группы к таблице на 16 слотов.
- **15-я запись: grow 16 → 32** показывает синхронное перехеширование выбранной таблицы.
- **29-я запись: split** уменьшает учебный предел таблицы до 32 и показывает разделение на две таблицы.
- **Несколько split** показывает directory и ситуацию, когда несколько индексов могут указывать на одну таблицу.
- Переключатель **«Подробный режим»** открывает формулы H1/H2 и битовые расчёты.

### 2. Эвакуация

Кнопка **«Поймать oldbuckets»** заполняет классическую модель ровно до наблюдаемого роста. Затем:

- выполните чтение и убедитесь, что `nevacuate` не меняется;
- выполните запись или удаление и проследите `growWork`;
- сравните старые X/Y-направления и новые бакеты.

### 3. Unsafe

Здесь операции выполняются над настоящей `map[int]int`. Инспектор показывает:

- `Map.used`, `dirPtr`, `dirLen`, `globalDepth`, случайный `seed`;
- реальные адреса таблиц;
- `capacity`, `growthLeft`, tombstones, localDepth;
- control word каждой группы и физическое положение пар;
- сколько существующих ключей сменили слот во время grow.

> `unsafe`-часть намеренно привязана к layout runtime. Это учебный микроскоп, а не подход для production-кода.

## Как посмотреть настоящий legacy runtime

На Go 1.24+ стандартная сборка использует Swiss Table. Чтобы unsafe-вкладка показывала настоящие `hmap`, `oldbuckets` и `nevacuate`, соберите приложение с отключённой Swiss Table:

```powershell
$env:GOEXPERIMENT = "noswissmap"
go run .
```

Помощник делает то же самое:

```powershell
.\run.ps1 -LegacyInspector
```

После завершения можно очистить переменную в текущем PowerShell:

```powershell
Remove-Item Env:GOEXPERIMENT
```

## Важное исправление формулы эвакуации

В старом runtime функция `growWork` выполняла:

1. эвакуацию старого бакета, соответствующего текущему ключу;
2. эвакуацию бакета `nevacuate`, если рост ещё продолжается.

Эти бакеты могут совпасть, а целевой бакет может быть уже готов. Поэтому фраза «каждая запись гарантированно переносит два бакета» неверна.

Для `N` старых бакетов:

```text
ceil(N / 2) <= число изменяющих операций до завершения <= N
```

Точное число зависит от хешей изменяемых ключей. Чтения дают нулевой прогресс. При этом одна эвакуация переносит всю overflow-цепочку выбранного бакета.

В современной Swiss Table прежней пары `oldbuckets/nevacuate` вообще нет. Одна таблица перестраивается целиком в вызвавшей grow операции, но размер отдельной таблицы ограничен 1024 слотами. Большая map разбита directory на несколько независимо растущих таблиц.

## Команды разработчика

Форматирование и тесты стандартной сборки:

```powershell
gofmt -w .
go test ./...
```

Тесты legacy-инспектора:

```powershell
$env:GOEXPERIMENT = "noswissmap"
go test ./...
```

Сборка одного `.exe`:

```powershell
go build -buildvcs=false -o go-map-lab.exe .
```

## Структура проекта

```text
go-map-internals-lab/
├── main.go                         HTTP-сервер и точка входа
├── internal/
│   ├── lab/
│   │   ├── swiss.go                безопасная модель Swiss Table
│   │   ├── legacy.go               безопасная модель старой эвакуации
│   │   ├── hash.go                 повторяемый учебный хеш
│   │   └── model.go                JSON-модель визуализации
│   ├── inspector/
│   │   ├── inspector_swiss.go      unsafe layout Go 1.24+
│   │   └── inspector_legacy.go     unsafe layout noswissmap
│   └── server/
│       ├── server.go               локальный API
│       └── static/                 HTML, CSS и JavaScript интерфейса
├── docs/
│   └── ARCHITECTURE.md             подробная связь модели с runtime
├── run.ps1                         удобный запуск в PowerShell
└── go.mod                          внешних зависимостей нет
```

## Первоисточники

- [Официальный блог Go: Faster Go maps with Swiss Tables](https://go.dev/blog/swisstable)
- [Go 1.24 release notes](https://go.dev/doc/go1.24)
- [Текущий `internal/runtime/maps/map.go`](https://go.dev/src/internal/runtime/maps/map.go)
- [Текущий `internal/runtime/maps/table.go`](https://go.dev/src/internal/runtime/maps/table.go)
- [Текущий `internal/runtime/maps/group.go`](https://go.dev/src/internal/runtime/maps/group.go)
- Legacy-реализация находится в исходниках toolchain как `runtime/map_noswiss.go`.
