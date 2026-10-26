param([switch]$Race, [int]$Count = 1)
$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot
if ($Count -lt 1) { throw 'Count must be positive' }
$options = @('-count=' + $Count)
if ($Race) { $options += '-race' }

# Explicit source filenames omit the requireAdministrator syso from test EXEs.
$main = Join-Path $project 'cmd/l4nfix'
$files = @(Get-ChildItem -LiteralPath $main -File -Filter '*.go' | ForEach-Object { $_.Name })
& go -C $main test @options @files
if ($LASTEXITCODE -ne 0) { throw "Main tests failed: exit=$LASTEXITCODE" }
& go -C $project test @options ./cmd/fontchange ./cmd/l4nfontchange ./cmd/makeicon ./internal/...
if ($LASTEXITCODE -ne 0) { throw "Companion/internal tests failed: exit=$LASTEXITCODE" }
$previousArch = $env:GOARCH
try {
    $env:GOARCH = '386'
    # Go's race detector does not support windows/386. Keep race coverage on
    # the main/internal amd64 run above, then run the x86 ABI fixtures normally.
    & go -C $project test "-count=$Count" ./cmd/workshopsubscribe ./internal/steamsubscription
    if ($LASTEXITCODE -ne 0) { throw "x86 Steam bridge tests/build failed: exit=$LASTEXITCODE" }
} finally {
    $env:GOARCH = $previousArch
}
