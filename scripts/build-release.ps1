$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$env:GO111MODULE = 'on'
$env:GOCACHE = Join-Path $projectRoot '.gocache'
$env:GOMODCACHE = Join-Path $projectRoot '.gomodcache'

$targets = @(
    @('386', '386', ''),
    @('amd64', 'amd64', ''),
    @('arm64', 'arm64', ''),
    @('armv5', 'arm', '5'),
    @('armv6', 'arm', '6'),
    @('armv7', 'arm', '7'),
    @('s390x', 's390x', '')
)

Push-Location $projectRoot
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    $env:GOOS = 'linux'
    $env:CGO_ENABLED = '0'
    foreach ($target in $targets) {
        $asset, $architecture, $armVersion = $target
        $env:GOARCH = $architecture
        if ($armVersion) { $env:GOARM = $armVersion } else { Remove-Item Env:GOARM -ErrorAction SilentlyContinue }
        $binary = "dist\m-ui-linux-$asset"
        $archive = "$binary.tar.gz"
        go build -trimpath -ldflags='-s -w' -o $binary .
        if ($LASTEXITCODE -ne 0) { throw "build failed: $asset" }
        tar -czf $archive -C dist "m-ui-linux-$asset"
        if ($LASTEXITCODE -ne 0) { throw "package failed: $asset" }
        Write-Host "Built $archive"
    }
}
finally {
    Pop-Location
    Remove-Item Env:GOOS, Env:GOARCH, Env:GOARM, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
