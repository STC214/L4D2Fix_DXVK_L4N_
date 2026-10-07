param(
    [string]$GameExe,
    [string]$BackupRoot,
    [switch]$KeepBackup
)

$ErrorActionPreference = "Stop"
$PackageRoot = Split-Path -Parent $MyInvocation.MyCommand.Path

function Write-Info($Message) {
    Write-Host "[L4N-Restore] $Message"
}

function Is-RealGameExe($Path) {
    $full = [System.IO.Path]::GetFullPath($Path)
    $package = [System.IO.Path]::GetFullPath($PackageRoot)
    if ($full.StartsWith($package, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $false
    }

    $parent = Split-Path -Parent $full
    if (Test-Path (Join-Path $parent "left4dead2\pak01_dir.vpk")) {
        return $true
    }
    if (Test-Path (Join-Path $parent "left4dead2\gameinfo.txt")) {
        return $true
    }

    return $full -match "\\steamapps\\common\\Left 4 Dead 2\\left4dead2\.exe$"
}

function Get-SteamRoots {
    $roots = New-Object System.Collections.Generic.List[string]
    $regPaths = @(
        "HKCU:\Software\Valve\Steam",
        "HKLM:\SOFTWARE\WOW6432Node\Valve\Steam",
        "HKLM:\SOFTWARE\Valve\Steam"
    )

    foreach ($regPath in $regPaths) {
        try {
            $value = (Get-ItemProperty -Path $regPath -ErrorAction Stop).SteamPath
            if ($value) { $roots.Add($value) }
        } catch {}
        try {
            $value = (Get-ItemProperty -Path $regPath -ErrorAction Stop).InstallPath
            if ($value) { $roots.Add($value) }
        } catch {}
    }

    foreach ($path in @((Join-Path ${env:ProgramFiles(x86)} "Steam"), (Join-Path $env:ProgramFiles "Steam"))) {
        if ($path) { $roots.Add($path) }
    }

    return $roots | Where-Object { $_ -and (Test-Path $_) } | ForEach-Object {
        (Resolve-Path $_).Path
    } | Select-Object -Unique
}

function Read-SteamLibraryPaths($SteamRoot) {
    $paths = New-Object System.Collections.Generic.List[string]
    if ($SteamRoot -and (Test-Path $SteamRoot)) {
        $paths.Add($SteamRoot)
    }

    $libraryFile = Join-Path $SteamRoot "steamapps\libraryfolders.vdf"
    if (Test-Path $libraryFile) {
        $text = Get-Content -Raw -LiteralPath $libraryFile
        foreach ($match in [regex]::Matches($text, '"path"\s+"([^"]+)"')) {
            $path = $match.Groups[1].Value.Replace("\\", "\")
            if (Test-Path $path) { $paths.Add($path) }
        }
    }

    return $paths | Select-Object -Unique
}

function Find-GameExeFromSteam($SteamRoot) {
    foreach ($library in Read-SteamLibraryPaths $SteamRoot) {
        $manifest = Join-Path $library "steamapps\appmanifest_550.acf"
        if (Test-Path $manifest) {
            $text = Get-Content -Raw -LiteralPath $manifest
            $installDir = "Left 4 Dead 2"
            $match = [regex]::Match($text, '"installdir"\s+"([^"]+)"')
            if ($match.Success) {
                $installDir = $match.Groups[1].Value
            }
            $candidate = Join-Path $library ("steamapps\common\" + $installDir + "\left4dead2.exe")
            if (Test-Path $candidate) {
                return (Resolve-Path $candidate).Path
            }
        }

        $fallback = Join-Path $library "steamapps\common\Left 4 Dead 2\left4dead2.exe"
        if (Test-Path $fallback) {
            return (Resolve-Path $fallback).Path
        }
    }

    return $null
}

function Search-ByEverythingCli {
    $toolsDir = Join-Path $PackageRoot "tools"
    $candidates = @(
        (Join-Path $toolsDir "Everything\es.exe"),
        (Join-Path $toolsDir "es.exe"),
        (Join-Path ${env:ProgramFiles} "Everything\es.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "Everything\es.exe")
    )

    foreach ($es in $candidates) {
        if (Test-Path $es) {
            try {
                $results = & $es -n 50 left4dead2.exe 2>$null
                foreach ($item in $results) {
                    if ($item -and (Test-Path $item) -and (Is-RealGameExe $item)) {
                        return (Resolve-Path $item).Path
                    }
                }
            } catch {}
        }
    }

    return $null
}

function Search-ByEmbeddedIndex {
    $roots = New-Object System.Collections.Generic.List[string]
    foreach ($steamRoot in Get-SteamRoots) {
        foreach ($library in Read-SteamLibraryPaths $steamRoot) {
            $common = Join-Path $library "steamapps\common"
            if (Test-Path $common) { $roots.Add($common) }
        }
    }

    $driveRoots = Get-PSDrive -PSProvider FileSystem | Where-Object { $_.Free -ne $null } | ForEach-Object { $_.Root }
    foreach ($drive in $driveRoots) {
        foreach ($relative in @("SteamLibrary\steamapps\common", "Program Files (x86)\Steam\steamapps\common", "Program Files\Steam\steamapps\common")) {
            $candidate = Join-Path $drive $relative
            if (Test-Path $candidate) { $roots.Add($candidate) }
        }
    }

    foreach ($root in ($roots | Select-Object -Unique)) {
        try {
            $matches = Get-ChildItem -LiteralPath $root -Filter "left4dead2.exe" -Recurse -File -ErrorAction SilentlyContinue
            foreach ($match in $matches) {
                if (Is-RealGameExe $match.FullName) {
                    return (Resolve-Path $match.FullName).Path
                }
            }
        } catch {}
    }

    return $null
}

function Resolve-BackupRoot {
    if ($BackupRoot) {
        if (-not (Test-Path $BackupRoot)) {
            throw "BackupRoot does not exist: $BackupRoot"
        }
        return (Resolve-Path $BackupRoot).Path
    }

    if ($GameExe) {
        if (-not (Test-Path $GameExe)) {
            throw "GameExe does not exist: $GameExe"
        }
        $resolved = (Resolve-Path $GameExe).Path
        return Join-Path (Split-Path -Parent $resolved) ".l4n_auto_backup"
    }

    foreach ($steamRoot in Get-SteamRoots) {
        $found = Find-GameExeFromSteam $steamRoot
        if ($found -and (Is-RealGameExe $found)) {
            return Join-Path (Split-Path -Parent $found) ".l4n_auto_backup"
        }
    }

    $found = Search-ByEverythingCli
    if ($found) {
        return Join-Path (Split-Path -Parent $found) ".l4n_auto_backup"
    }

    $found = Search-ByEmbeddedIndex
    if ($found) {
        return Join-Path (Split-Path -Parent $found) ".l4n_auto_backup"
    }

    throw "Could not locate the backup directory. Use -GameExe or -BackupRoot to specify it."
}

function Remove-EmptyParents($Path, $StopAt) {
    $dir = Split-Path -Parent $Path
    $stop = [System.IO.Path]::GetFullPath($StopAt).TrimEnd([char]'\')
    while ($dir -and ([System.IO.Path]::GetFullPath($dir).TrimEnd([char]'\') -ine $stop)) {
        try {
            $items = Get-ChildItem -LiteralPath $dir -Force -ErrorAction Stop
            if ($items.Count -gt 0) { return }
            Remove-Item -LiteralPath $dir -Force
        } catch {
            return
        }
        $dir = Split-Path -Parent $dir
    }
}

$resolvedBackupRoot = Resolve-BackupRoot
$manifestPath = Join-Path $resolvedBackupRoot "manifest.json"
if (-not (Test-Path $manifestPath)) {
    throw "Backup manifest was not found: $manifestPath"
}

$manifest = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
$gameRoot = $manifest.gameRoot
if (-not $gameRoot -or -not (Test-Path $gameRoot)) {
    throw "The game directory recorded in the manifest does not exist: $gameRoot"
}

Write-Info "Using backup manifest: $manifestPath"
Write-Info "Restoring game directory: $gameRoot"

foreach ($entry in @($manifest.files)) {
    $target = $entry.target
    if ($entry.existed) {
        if (-not $entry.backup -or -not (Test-Path $entry.backup)) {
            Write-Info "Missing backup; skipped: $target"
            continue
        }
        $targetDir = Split-Path -Parent $target
        if (-not (Test-Path $targetDir)) {
            New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
        }
        Copy-Item -LiteralPath $entry.backup -Destination $target -Force
        Write-Info "Restored: $target"
    } else {
        if (Test-Path -LiteralPath $target) {
            Remove-Item -LiteralPath $target -Force
            Write-Info "Removed file added by installer: $target"
            Remove-EmptyParents $target $gameRoot
        }
    }
}

foreach ($entry in @($manifest.steamConfigs)) {
    if ($entry.existed -and $entry.backup -and (Test-Path $entry.backup)) {
        $targetDir = Split-Path -Parent $entry.target
        if (-not (Test-Path $targetDir)) {
            New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
        }
        Copy-Item -LiteralPath $entry.backup -Destination $entry.target -Force
        Write-Info "Restored Steam config: $($entry.target)"
    }
}

if (-not $KeepBackup) {
    Remove-Item -LiteralPath $resolvedBackupRoot -Recurse -Force
    Write-Info "Removed backup directory."
} else {
    Write-Info "Backup directory kept because -KeepBackup was used."
}

Write-Info "Restore complete."
