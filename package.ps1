# Compatibility entry point; implementation lives under scripts/.
param([switch]$SkipBuild)
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts/package.ps1') -SkipBuild:$SkipBuild
