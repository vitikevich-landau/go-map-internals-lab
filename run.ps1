param(
    [ValidateRange(1, 65535)]
    [int]$Port = 8080,

    [switch]$LegacyInspector
)

$ErrorActionPreference = "Stop"
$projectDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -LiteralPath $projectDirectory

if ($LegacyInspector) {
    $env:GOEXPERIMENT = "noswissmap"
    Write-Host "Unsafe-инспектор: legacy hmap/oldbuckets/nevacuate" -ForegroundColor Yellow
} else {
    Remove-Item Env:GOEXPERIMENT -ErrorAction SilentlyContinue
    Write-Host "Unsafe-инспектор: Swiss Table текущего Go" -ForegroundColor Cyan
}

Write-Host "Откройте http://127.0.0.1:$Port" -ForegroundColor Green
Write-Host "Остановка: Ctrl+C"
go run . -addr "127.0.0.1:$Port"
