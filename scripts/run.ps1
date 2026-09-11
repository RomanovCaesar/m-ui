$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$env:GO111MODULE = 'on'
$env:GOCACHE = Join-Path $projectRoot '.gocache'
$env:GOMODCACHE = Join-Path $projectRoot '.gomodcache'
Push-Location $projectRoot
try {
    go run .
}
finally {
    Pop-Location
}
