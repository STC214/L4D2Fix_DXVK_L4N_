# Compatibility entry point; implementation lives under scripts/.
param([string]$OutputPath = 'L4N_Go_Win32_Portable/L4N_Go_Win32.exe')
$ErrorActionPreference = 'Stop'
& (Join-Path $PSScriptRoot 'scripts/build.ps1') -OutputPath $OutputPath
