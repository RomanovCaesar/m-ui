$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$env:GO111MODULE = 'on'
$env:GOCACHE = Join-Path $projectRoot '.gocache'
$env:GOMODCACHE = Join-Path $projectRoot '.gomodcache'
Push-Location $projectRoot
try {
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    go build -trimpath -ldflags='-s -w' -o m-ui.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Windows build failed' }
    go build -trimpath -ldflags='-s -w' -o dist\m-ui.exe .
    if ($LASTEXITCODE -ne 0) { throw 'Windows dist build failed' }
    $env:GOOS = 'linux'
    $env:GOARCH = 'amd64'
    $env:CGO_ENABLED = '0'
    go build -trimpath -ldflags='-s -w' -o dist\m-ui-linux-amd64 .
    if ($LASTEXITCODE -ne 0) { throw 'Linux build failed' }
    Write-Host 'Built m-ui.exe, dist\m-ui.exe and dist\m-ui-linux-amd64'
}
finally {
    Pop-Location
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
