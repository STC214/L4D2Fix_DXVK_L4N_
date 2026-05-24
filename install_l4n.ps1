param(
    [string]$GameExe,
    [string]$SteamPath,
    [switch]$SkipRuntime,
    [switch]$SkipSteamLaunchOptions,
    [switch]$ForceAmdDxvk,
    [switch]$ForceGenericDxvk
)

$ErrorActionPreference = "Stop"

$PackageRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$DefaultLaunchOptions = "-heapsize 2097152 -processheap -high -novid -nojoy -steam -vulkan -lv"

function Write-Info($Message) {
    Write-Host "[L4N] $Message"
}

function Find-PackageDirByFiles($RequiredRelativeFiles) {
    $dirs = @(Get-ChildItem -LiteralPath $PackageRoot -Directory -Force) + @((Get-Item -LiteralPath $PackageRoot))
    foreach ($dir in $dirs) {
        $ok = $true
        foreach ($relative in $RequiredRelativeFiles) {
            if (-not (Test-Path (Join-Path $dir.FullName $relative))) {
                $ok = $false
                break
            }
        }
        if ($ok) {
            return $dir.FullName
        }
    }
    return $null
}

function Find-LaunchOptionFile {
    $files = Get-ChildItem -LiteralPath $PackageRoot -File -Force | Where-Object {
        $_.Name -like "*.txt" -and $_.Name -notlike "*validate*" -and $_.Name -notlike "*verify*"
    }
    foreach ($file in $files) {
        $text = Get-Content -Raw -LiteralPath $file.FullName -ErrorAction SilentlyContinue
        if ($text -match "-heapsize" -and $text -match "-vulkan") {
            return $file.FullName
        }
    }
    return $null
}

$RuntimeDir = Find-PackageDirByFiles @("VC_redist.x86.exe", "VC_redist.x64.exe")
$DxvkDir = Find-PackageDirByFiles @("dxgi.dll", "dxvk_d3d9.dll", "bin\dxvk_d3d9.dll")
$L4nDir = Find-PackageDirByFiles @("readme_l4n.txt", "bin\left4neko.dll")
$LaunchOptionFile = Find-LaunchOptionFile

function Get-RelativePathCompat($BasePath, $TargetPath) {
    $base = [System.IO.Path]::GetFullPath($BasePath)
    $target = [System.IO.Path]::GetFullPath($TargetPath)
    if (-not $base.EndsWith([System.IO.Path]::DirectorySeparatorChar)) {
        $base += [System.IO.Path]::DirectorySeparatorChar
    }

    $baseUri = [Uri]$base
    $targetUri = [Uri]$target
    $relative = $baseUri.MakeRelativeUri($targetUri).ToString()
    return [Uri]::UnescapeDataString($relative).Replace("/", [System.IO.Path]::DirectorySeparatorChar)
}

function ConvertTo-VdfEscaped($Text) {
    return $Text.Replace("\", "\\").Replace('"', '\"')
}

function Get-SteamRoots {
    $roots = New-Object System.Collections.Generic.List[string]

    if ($SteamPath) {
        $roots.Add($SteamPath)
    }

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

    $common = @(
        (Join-Path ${env:ProgramFiles(x86)} "Steam"),
        (Join-Path $env:ProgramFiles "Steam")
    )
    foreach ($path in $common) {
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
    $bundledEverything = Join-Path $PackageRoot "tools\Everything\Everything.exe"
    if (Test-Path $bundledEverything) {
        try {
            Write-Info "Starting bundled Everything engine."
            Start-Process -FilePath $bundledEverything -ArgumentList "-startup" -WindowStyle Hidden | Out-Null
            Start-Sleep -Seconds 3
        } catch {
            Write-Info "Could not start bundled Everything: $($_.Exception.Message)"
        }
    }

    $toolsDir = Join-Path $PackageRoot "tools"
    $candidates = @(
        (Join-Path $toolsDir "Everything\es.exe"),
        (Join-Path $toolsDir "es.exe"),
        (Join-Path ${env:ProgramFiles} "Everything\es.exe"),
        (Join-Path ${env:ProgramFiles(x86)} "Everything\es.exe")
    )

    foreach ($es in $candidates) {
        if (Test-Path $es) {
            Write-Info "Searching left4dead2.exe with Everything CLI: $es"
            try {
                for ($attempt = 1; $attempt -le 3; $attempt++) {
                    $results = & $es -n 50 left4dead2.exe 2>$null
                    foreach ($item in $results) {
                        if ($item -and (Test-Path $item) -and (Is-RealGameExe $item)) {
                            return (Resolve-Path $item).Path
                        }
                    }
                    Start-Sleep -Seconds 2
                }
            } catch {}
        }
    }

    return $null
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

function Search-ByEmbeddedIndex {
    Write-Info "Everything CLI not found. Using the embedded fast filesystem indexer."

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

function Resolve-GameExe {
    if ($GameExe) {
        if (-not (Test-Path $GameExe)) {
            throw "GameExe does not exist: $GameExe"
        }
        $resolved = (Resolve-Path $GameExe).Path
        if (-not (Is-RealGameExe $resolved)) {
            throw "The specified left4dead2.exe does not look like the real game executable: $resolved"
        }
        return $resolved
    }

    foreach ($steamRoot in Get-SteamRoots) {
        $found = Find-GameExeFromSteam $steamRoot
        if ($found -and (Is-RealGameExe $found)) {
            return $found
        }
    }

    $found = Search-ByEverythingCli
    if ($found) { return $found }

    $found = Search-ByEmbeddedIndex
    if ($found) { return $found }

    throw "Could not auto-detect the real left4dead2.exe. Use -GameExe `"full\path\left4dead2.exe`" to specify it."
}

function Load-Manifest($BackupRoot, $GameRoot) {
    $manifestPath = Join-Path $BackupRoot "manifest.json"
    if (Test-Path $manifestPath) {
        return Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
    }

    return [pscustomobject]@{
        createdAt = (Get-Date).ToString("s")
        gameRoot = $GameRoot
        files = @()
        steamConfigs = @()
        launchOptions = $null
        dxvkFlavor = $null
    }
}

function Save-Manifest($Manifest, $BackupRoot) {
    if (-not (Test-Path $BackupRoot)) {
        New-Item -ItemType Directory -Path $BackupRoot | Out-Null
    }
    $manifestPath = Join-Path $BackupRoot "manifest.json"
    $Manifest | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath $manifestPath -Encoding UTF8
}

function Has-ManifestFileEntry($Manifest, $Target) {
    foreach ($entry in @($Manifest.files)) {
        if ($entry.target -and ([System.IO.Path]::GetFullPath($entry.target) -ieq [System.IO.Path]::GetFullPath($Target))) {
            return $true
        }
    }
    return $false
}

function Add-ArrayItem($Object, $PropertyName, $Item) {
    $current = @($Object.$PropertyName)
    $Object.$PropertyName = @($current + $Item)
}

function Backup-TargetFile($Manifest, $BackupRoot, $GameRoot, $Target) {
    if (Has-ManifestFileEntry $Manifest $Target) {
        return
    }

    $relative = Get-RelativePathCompat $GameRoot $Target
    $backupPath = Join-Path (Join-Path $BackupRoot "files") $relative
    $existed = Test-Path -LiteralPath $Target -PathType Leaf

    if ($existed) {
        $backupDir = Split-Path -Parent $backupPath
        if (-not (Test-Path $backupDir)) {
            New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
        }
        Copy-Item -LiteralPath $Target -Destination $backupPath -Force
    }

    Add-ArrayItem $Manifest "files" ([pscustomobject]@{
        target = $Target
        relative = $relative
        existed = $existed
        backup = $(if ($existed) { $backupPath } else { $null })
    })
}

function Copy-WithBackup($Manifest, $BackupRoot, $GameRoot, $Source, $Target) {
    Backup-TargetFile $Manifest $BackupRoot $GameRoot $Target
    $targetDir = Split-Path -Parent $Target
    if (-not (Test-Path $targetDir)) {
        New-Item -ItemType Directory -Path $targetDir -Force | Out-Null
    }
    Copy-Item -LiteralPath $Source -Destination $Target -Force
}

function Install-Runtimes {
    if ($SkipRuntime) {
        Write-Info "Step 1 skipped: runtime installation."
        return
    }

    Write-Info "Step 1: installing both VC++ runtimes. Unsupported or already-installed runtimes are skipped."
    foreach ($exe in @("VC_redist.x86.exe", "VC_redist.x64.exe")) {
        $path = Join-Path $RuntimeDir $exe
        if (Test-Path $path) {
            Write-Info "Running $exe"
            $process = Start-Process -FilePath $path -ArgumentList "/install /quiet /norestart" -Wait -PassThru
            if ($process.ExitCode -eq 0) {
                Write-Info "$exe installed or repaired successfully."
            } elseif ($process.ExitCode -in @(1638, 3010, 5100)) {
                Write-Info "$exe returned exit code $($process.ExitCode); treated as already installed, reboot required, or unsupported."
            } else {
                Write-Info "$exe returned exit code $($process.ExitCode); continuing with the remaining steps."
            }
        } else {
            Write-Info "$exe not found; skipped."
        }
    }
}

function Use-AmdDxvk {
    if ($ForceAmdDxvk -and $ForceGenericDxvk) {
        throw "Do not use -ForceAmdDxvk and -ForceGenericDxvk together."
    }
    if ($ForceAmdDxvk) { return $true }
    if ($ForceGenericDxvk) { return $false }

    try {
        $gpus = Get-CimInstance Win32_VideoController | ForEach-Object { "$($_.Name) $($_.AdapterCompatibility)" }
        foreach ($gpu in $gpus) {
            if ($gpu -match "AMD|Radeon|Advanced Micro Devices") {
                return $true
            }
        }
    } catch {}

    return $false
}

function Resolve-DxvkSourceDir($Amd) {
    if ($Amd) {
        $amdDir = Get-ChildItem -LiteralPath $DxvkDir -Directory -Force -ErrorAction SilentlyContinue |
            Where-Object {
                (Test-Path (Join-Path $_.FullName "x32\dxvk_d3d9.dll")) -or
                ((Test-Path (Join-Path $_.FullName "dxgi.dll")) -and (Test-Path (Join-Path $_.FullName "dxvk_d3d9.dll")))
            } |
            Select-Object -First 1

        if ($amdDir) {
            $x32 = Join-Path $amdDir.FullName "x32"
            if (Test-Path $x32) { return $x32 }
            return $amdDir.FullName
        }

        Write-Info "AMD GPU detected, but no AMD-specific DXVK folder was found; using the bundled generic DXVK files."
    }

    $x32 = Join-Path $DxvkDir "x32"
    if ((Test-Path (Join-Path $x32 "dxgi.dll")) -and (Test-Path (Join-Path $x32 "dxvk_d3d9.dll"))) {
        return $x32
    }

    if ((Test-Path (Join-Path $DxvkDir "dxgi.dll")) -and (Test-Path (Join-Path $DxvkDir "dxvk_d3d9.dll"))) {
        return $DxvkDir
    }

    throw "DXVK source files were not found under: $DxvkDir"
}

function Install-Dxvk($Manifest, $BackupRoot, $GameRoot) {
    $amd = Use-AmdDxvk
    $sourceDir = Resolve-DxvkSourceDir $amd

    if (-not (Test-Path $sourceDir)) {
        throw "DXVK source directory does not exist: $sourceDir"
    }

    $Manifest.dxvkFlavor = $(if ($amd) { "amd" } else { "generic" })
    Write-Info ("Step 2: copying DXVK ({0})." -f $Manifest.dxvkFlavor)

    foreach ($dll in @("dxgi.dll", "dxvk_d3d9.dll")) {
        Copy-WithBackup $Manifest $BackupRoot $GameRoot (Join-Path $sourceDir $dll) (Join-Path $GameRoot $dll)
    }

    Copy-WithBackup $Manifest $BackupRoot $GameRoot (Join-Path $sourceDir "dxvk_d3d9.dll") (Join-Path $GameRoot "bin\dxvk_d3d9.dll")
}

function Install-L4n($Manifest, $BackupRoot, $GameRoot) {
    Write-Info "Step 3: copying L4N files into the game root."

    $files = Get-ChildItem -LiteralPath $L4nDir -Recurse -File -Force
    foreach ($file in $files) {
        $relative = Get-RelativePathCompat $L4nDir $file.FullName
        $target = Join-Path $GameRoot $relative
        Copy-WithBackup $Manifest $BackupRoot $GameRoot $file.FullName $target
    }
}

function Find-MatchingBrace($Text, $OpenIndex) {
    $depth = 0
    $inString = $false
    for ($i = $OpenIndex; $i -lt $Text.Length; $i++) {
        $ch = $Text[$i]
        if ($ch -eq '"' -and ($i -eq 0 -or $Text[$i - 1] -ne "\")) {
            $inString = -not $inString
            continue
        }
        if ($inString) { continue }
        if ($ch -eq "{") { $depth++ }
        elseif ($ch -eq "}") {
            $depth--
            if ($depth -eq 0) { return $i }
        }
    }
    return -1
}

function Set-AppLaunchOptionsInText($Text, $Options) {
    $escapedOptions = ConvertTo-VdfEscaped $Options
    $appMatch = [regex]::Match($Text, '"550"\s*\{')
    if ($appMatch.Success) {
        $openIndex = $Text.IndexOf("{", $appMatch.Index)
        $closeIndex = Find-MatchingBrace $Text $openIndex
        if ($closeIndex -lt 0) {
            throw "Invalid AppID 550 block in localconfig.vdf."
        }

        $before = $Text.Substring(0, $openIndex + 1)
        $block = $Text.Substring($openIndex + 1, $closeIndex - $openIndex - 1)
        $after = $Text.Substring($closeIndex)
        $launchPattern = '(?m)^(\s*)"LaunchOptions"\s*"[^"]*"'

        if ([regex]::IsMatch($block, $launchPattern)) {
            $block = [regex]::Replace($block, $launchPattern, ('$1"LaunchOptions"' + "`t`"" + $escapedOptions + "`""))
        } else {
            $block = $block.TrimEnd() + "`r`n`t`t`t`t`t`t`"LaunchOptions`"`t`"$escapedOptions`"`r`n"
        }

        return $before + $block + $after
    }

    $appsMatch = [regex]::Match($Text, '"apps"\s*\{')
    if ($appsMatch.Success) {
        $openIndex = $Text.IndexOf("{", $appsMatch.Index)
        $closeIndex = Find-MatchingBrace $Text $openIndex
        if ($closeIndex -lt 0) {
            throw "Invalid apps block in localconfig.vdf."
        }

        $insert = "`r`n`t`t`t`t`t`"550`"`r`n`t`t`t`t`t{`r`n`t`t`t`t`t`t`"LaunchOptions`"`t`"$escapedOptions`"`r`n`t`t`t`t`t}`r`n"
        return $Text.Substring(0, $closeIndex) + $insert + $Text.Substring($closeIndex)
    }

    throw "Could not find the apps block in localconfig.vdf. Set these launch options manually in Steam: $Options"
}

function Backup-SteamConfig($Manifest, $BackupRoot, $ConfigPath) {
    foreach ($entry in @($Manifest.steamConfigs)) {
        if ($entry.target -and ([System.IO.Path]::GetFullPath($entry.target) -ieq [System.IO.Path]::GetFullPath($ConfigPath))) {
            return
        }
    }

    $name = ([System.IO.Path]::GetFullPath($ConfigPath) -replace '[:\\\/]', '_')
    $backupPath = Join-Path (Join-Path $BackupRoot "steam") $name
    $backupDir = Split-Path -Parent $backupPath
    if (-not (Test-Path $backupDir)) {
        New-Item -ItemType Directory -Path $backupDir -Force | Out-Null
    }
    Copy-Item -LiteralPath $ConfigPath -Destination $backupPath -Force

    Add-ArrayItem $Manifest "steamConfigs" ([pscustomobject]@{
        target = $ConfigPath
        existed = $true
        backup = $backupPath
    })
}

function Test-SteamRunning {
    return [bool](Get-Process -Name "steam" -ErrorAction SilentlyContinue)
}

function Set-SteamLaunchOptions($Manifest, $BackupRoot, $Options) {
    if ($SkipSteamLaunchOptions) {
        Write-Info "Step 4 skipped: Steam launch options."
        return
    }

    Write-Info "Step 4: writing Steam launch options."
    if (Test-SteamRunning) {
        Write-Info "Steam is currently running. If Steam overwrites this file on exit, close Steam completely and run this installer again."
    }

    $updated = 0
    foreach ($steamRoot in Get-SteamRoots) {
        $userdata = Join-Path $steamRoot "userdata"
        if (-not (Test-Path $userdata)) { continue }

        $configs = Get-ChildItem -LiteralPath $userdata -Filter "localconfig.vdf" -Recurse -File -ErrorAction SilentlyContinue
        foreach ($config in $configs) {
            try {
                $text = Get-Content -Raw -LiteralPath $config.FullName
                $newText = Set-AppLaunchOptionsInText $text $Options
                if ($newText -ne $text) {
                    Backup-SteamConfig $Manifest $BackupRoot $config.FullName
                    Set-Content -LiteralPath $config.FullName -Value $newText -Encoding UTF8
                    $updated++
                    Write-Info "Updated: $($config.FullName)"
                }
            } catch {
                Write-Info "Could not update $($config.FullName): $($_.Exception.Message)"
            }
        }
    }

    if ($updated -eq 0) {
        Write-Info "No Steam launch option file was updated. Set this manually in Steam: $Options"
    }
}

foreach ($required in @(
    [pscustomobject]@{ Name = "runtime package"; Path = $RuntimeDir },
    [pscustomobject]@{ Name = "DXVK package"; Path = $DxvkDir },
    [pscustomobject]@{ Name = "L4N package"; Path = $L4nDir }
)) {
    if (-not $required.Path -or -not (Test-Path $required.Path)) {
        throw "Missing $($required.Name) directory."
    }
}

$resolvedGameExe = Resolve-GameExe
$gameRoot = Split-Path -Parent $resolvedGameExe
$backupRoot = Join-Path $gameRoot ".l4n_auto_backup"
$manifest = Load-Manifest $backupRoot $gameRoot
$launchOptions = if (Test-Path $LaunchOptionFile) {
    (Get-Content -Raw -LiteralPath $LaunchOptionFile).Trim()
} else {
    $DefaultLaunchOptions
}
if (-not $launchOptions) {
    $launchOptions = $DefaultLaunchOptions
}
$manifest.launchOptions = $launchOptions

Write-Info "Detected game executable: $resolvedGameExe"
Write-Info "Backup manifest: $(Join-Path $backupRoot 'manifest.json')"

Install-Runtimes
Install-Dxvk $manifest $backupRoot $gameRoot
Install-L4n $manifest $backupRoot $gameRoot
Set-SteamLaunchOptions $manifest $backupRoot $launchOptions
Save-Manifest $manifest $backupRoot

Write-Info "Done. In game console, verify with mat_info and mem_dump."
