param(
    [ValidateRange(1, 65535)]
    [int]$Port = 8080,

    [switch]$LegacyInspector
)

# Любая ошибка команды немедленно завершает запуск, а не оставляет скрипт в
# частично настроенном состоянии.
$ErrorActionPreference = "Stop"

# Рабочая директория всегда совпадает с корнем проекта независимо от того, откуда
# пользователь вызвал run.ps1.
$projectDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -LiteralPath $projectDirectory

# Переменная GOEXPERIMENT выбирает один из двух взаимоисключающих build-вариантов
# unsafe-инспектора. Без переключателя используется стандартная Swiss Table.
if ($LegacyInspector) {
    $env:GOEXPERIMENT = "noswissmap"
    Write-Host "Unsafe-инспектор: legacy hmap/oldbuckets/nevacuate" -ForegroundColor Yellow
} else {
    Remove-Item Env:GOEXPERIMENT -ErrorAction SilentlyContinue
    Write-Host "Unsafe-инспектор: Swiss Table текущего Go" -ForegroundColor Cyan
}

# Сервер привязан только к loopback-интерфейсу и не публикуется во внешнюю сеть.
Write-Host "Откройте http://127.0.0.1:$Port" -ForegroundColor Green
Write-Host "Остановка: Ctrl+C"
go run . -addr "127.0.0.1:$Port"
