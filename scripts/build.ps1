param([string]$OutputPath = 'L4N_Go_Win32_Portable/L4N_Go_Win32.exe')
$ErrorActionPreference = 'Stop'

$project = Split-Path -Parent $PSScriptRoot
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
    & go build -ldflags="-H=windowsgui -X main.buildVersion=$version" -o $staging ./cmd/l4nfix
    if ($LASTEXITCODE -ne 0) { throw "go build failed: exit=$LASTEXITCODE" }
    # Rebuild companion tools too; packages must not contain stale font binaries.
    $companions = @(
        @{ Name='L4D2_Font_Change.exe'; Package='./cmd/fontchange' },
        @{ Name='L4N_Font_Change.exe'; Package='./cmd/l4nfontchange' }
    )
    # L4D2's bundled Steam API is x86 even when the main tool is x64.
    $companions += @{ Name='Steam_Workshop_Subscribe.exe'; Package='./cmd/workshopsubscribe'; Arch='386' }
    $fontStaging = @()
    try {
        foreach ($tool in $companions) {
            $temp = Join-Path (Split-Path $OutputPath) ('.build-' + [Guid]::NewGuid().ToString('N') + '.exe')
            $fontStaging += @{ Temp=$temp; Output=(Join-Path (Split-Path $OutputPath) $tool.Name) }
            $previousArch = $env:GOARCH
            try {
                if ($tool.Arch) { $env:GOARCH = $tool.Arch }
                if ($tool.Arch) {
                    # Bridge uses captured stdout; parent hides its console.
                    & go build -o $temp $tool.Package
                } else {
                    & go build -ldflags='-H=windowsgui' -o $temp $tool.Package
                }
            } finally {
                $env:GOARCH = $previousArch
            }
            if ($LASTEXITCODE -ne 0) { throw "Companion build failed: $($tool.Name), exit=$LASTEXITCODE" }
        }
        foreach ($tool in $fontStaging) {
            Copy-Item -LiteralPath $tool.Temp -Destination $tool.Output -Force
            Write-Output "Companion: $($tool.Output) SHA256=$((Get-FileHash -LiteralPath $tool.Output -Algorithm SHA256).Hash)"
        }
    } finally {
        foreach ($tool in $fontStaging) {
            if (Test-Path -LiteralPath $tool.Temp) { Remove-Item -LiteralPath $tool.Temp }
        }
    }
    Copy-Item -LiteralPath $staging -Destination $OutputPath -Force
    Write-Output "Build version: $version (Asia/Shanghai)"
    Write-Output "Window title: L4N $version"
    Write-Output "Output: $OutputPath"
    Write-Output "SHA256: $((Get-FileHash -LiteralPath $OutputPath -Algorithm SHA256).Hash)"
} finally {
    Pop-Location
    if (Test-Path -LiteralPath $staging) { Remove-Item -LiteralPath $staging }
}
