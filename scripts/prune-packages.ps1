[CmdletBinding(SupportsShouldProcess=$true)]
param(
    [string]$DistPath = (Join-Path (Split-Path -Parent $PSScriptRoot) 'dist'),
    [ValidateRange(1,100)][int]$Keep = 3
)
$ErrorActionPreference = 'Stop'
$directory = [IO.Path]::GetFullPath($DistPath)
if (-not (Test-Path -LiteralPath $directory -PathType Container)) { throw "Release directory missing: $directory" }
$boundary = $directory.TrimEnd('\','/') + [IO.Path]::DirectorySeparatorChar
$packages = @(Get-ChildItem -LiteralPath $directory -File | Where-Object {
    $_.Name -match '^L4N_Go_Win32_Portable(?:_\d{12})?\.zip$'
} | ForEach-Object {
    $version = [DateTime]::MinValue
    if ($_.Name -match '_(\d{12})\.zip$') {
        $version = [DateTime]::ParseExact($Matches[1], 'yyyyMMddHHmm', [Globalization.CultureInfo]::InvariantCulture)
    }
    [pscustomobject]@{ File=$_; Version=$version }
} | Sort-Object Version -Descending)

# Verify the complete deletion plan stays directly beneath the named directory.
$remove = @($packages | Select-Object -Skip $Keep)
foreach ($entry in $remove) {
    $path = [IO.Path]::GetFullPath($entry.File.FullName)
    if (-not $path.StartsWith($boundary,[StringComparison]::OrdinalIgnoreCase) -or
        -not [String]::Equals((Split-Path -Parent $path),$directory.TrimEnd('\','/'),[StringComparison]::OrdinalIgnoreCase)) {
        throw "Unexpected release deletion path: $path"
    }
}
foreach ($entry in $remove) {
    $file=$entry.File
    if ($PSCmdlet.ShouldProcess($file.FullName,'Remove old portable package')) {
        $hash=(Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash
        Remove-Item -LiteralPath $file.FullName -Force
        if (Test-Path -LiteralPath $file.FullName) { throw "Old package still exists: $($file.FullName)" }
        Write-Output "Removed: $($file.Name); bytes=$($file.Length); SHA256=$hash"
    }
}
foreach ($entry in ($packages | Select-Object -First $Keep)) { Write-Output "Retained: $($entry.File.Name)" }
