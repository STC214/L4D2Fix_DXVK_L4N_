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
