param([string]$OutputPath = 'L4N_Go_Win32_Portable/L4N_Go_Win32.exe')
$ErrorActionPreference = 'Stop'

$project = $PSScriptRoot
if (-not [IO.Path]::IsPathRooted($OutputPath)) {
    $OutputPath = Join-Path $project $OutputPath
}
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
New-Item -ItemType Directory -Path (Split-Path $OutputPath) -Force | Out-Null

# Use the client/project timezone regardless of the execution host timezone.
$zone = [TimeZoneInfo]::FindSystemTimeZoneById('China Standard Time')
$version = [TimeZoneInfo]::ConvertTimeFromUtc([DateTime]::UtcNow, $zone).ToString('yyyyMMddHHmm', [Globalization.CultureInfo]::InvariantCulture)

# Build to a sibling temporary file; a failed build leaves the current EXE intact.
$staging = Join-Path (Split-Path $OutputPath) ('.build-' + [Guid]::NewGuid().ToString('N') + '.exe')
Push-Location -LiteralPath $project
try {
    & go build -ldflags="-H=windowsgui -X main.buildVersion=$version" -o $staging .
    if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    Copy-Item -LiteralPath $staging -Destination $OutputPath -Force
    Write-Output "Build version: $version (Asia/Shanghai)"
    Write-Output "Window title: L4N $version"
    Write-Output "Output: $OutputPath"
    Write-Output "SHA256: $((Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash)"
} finally {
    Pop-Location
    if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging }
}
