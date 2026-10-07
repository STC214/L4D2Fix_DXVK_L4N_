param([switch]$SkipBuild)
$ErrorActionPreference='Stop'
function Test-ExcludedResourcePath([string]$Relative) {
 $excluded=@('.package_tmp','.l4n_auto_backup','addons_backup','display_settings_backup','.l4d2_font_change_backup','config_backups','addons_backup.tmp','addons_backup.old')
 foreach($segment in ($Relative -split '[\\/]')) {
  if($excluded -contains $segment -or $segment -like '.mods-backup-*' -or $segment -like '.display-backup-*' -or $segment -like '.replace-old-*' -or $segment -like '.pending-*' -or $segment -like '.copy-*.tmp' -or $segment -like '.write-*.tmp') { return $true }
 }
 return $false
}
$project=Split-Path -Parent $PSScriptRoot
$portable=Join-Path $project 'L4N_Go_Win32_Portable'
if(-not $SkipBuild){& (Join-Path $PSScriptRoot 'build.ps1');if($LASTEXITCODE -ne 0){throw 'Build failed'}}
$metadata=& go version -m (Join-Path $portable 'L4N_Go_Win32.exe')
if($LASTEXITCODE -ne 0){throw 'EXE metadata read failed'}
$match=[regex]::Match(($metadata -join "`n"),'main\.buildVersion=(\d{12})')
if(-not $match.Success){throw 'Use build.ps1 to embed the build version first'}
$version=$match.Groups[1].Value
$dist=Join-Path $project 'dist';New-Item -ItemType Directory $dist -Force | Out-Null
$output=Join-Path $dist "L4N_Go_Win32_Portable_$version.zip"
if(Test-Path -LiteralPath $output){throw "Package already exists: $output"}
$tempRoot=Join-Path $project '.gotmp';New-Item -ItemType Directory $tempRoot -Force | Out-Null
$stage=Join-Path $tempRoot ('package-'+[guid]::NewGuid().ToString('N'))
$target=Join-Path $stage 'L4N_Go_Win32_Portable'
New-Item -ItemType Directory $target -Force | Out-Null
$zipTemp=Join-Path $tempRoot ('.package-'+[guid]::NewGuid().ToString('N')+'.zip')
try{
 foreach($name in @('L4N_Go_Win32.exe','L4N_Font_Change.exe','L4D2_Font_Change.exe')){Copy-Item -LiteralPath (Join-Path $portable $name) -Destination $target}
 Copy-Item -LiteralPath (Join-Path $project 'docs/压缩包资源使用.md') -Destination (Join-Path $target '使用说明.md')
 $source=Join-Path $portable 'resources'
 foreach($f in Get-ChildItem -LiteralPath $source -Recurse -File){
  $relative=$f.FullName.Substring($source.Length+1)
  if(Test-ExcludedResourcePath $relative){continue}
  $dest=Join-Path (Join-Path $target 'resources') $relative
  New-Item -ItemType Directory (Split-Path $dest) -Force | Out-Null
  Copy-Item -LiteralPath $f.FullName -Destination $dest
 }
 foreach($folder in @('dxvk','l4n')){New-Item -ItemType Directory (Join-Path $target ('resources/'+$folder)) -Force | Out-Null}
 Add-Type -AssemblyName System.IO.Compression
 Add-Type -AssemblyName System.IO.Compression.FileSystem
 $writer=[IO.Compression.ZipFile]::Open($zipTemp,[IO.Compression.ZipArchiveMode]::Create)
 try{
  foreach($file in Get-ChildItem -LiteralPath $stage -Recurse -File){
   $name=$file.FullName.Substring($stage.Length+1).Replace('\','/')
   [void][IO.Compression.ZipFileExtensions]::CreateEntryFromFile($writer,$file.FullName,$name,[IO.Compression.CompressionLevel]::Optimal)
  }
  foreach($folder in @('dxvk','l4n')){[void]$writer.CreateEntry('L4N_Go_Win32_Portable/resources/'+$folder+'/')}
 }finally{$writer.Dispose()}
 # Verify every member against staging hashes before returning an artifact.
 $z=[IO.Compression.ZipFile]::OpenRead($zipTemp)
 $count=0
 try{
  foreach($entry in $z.Entries){if($entry.FullName.EndsWith('/')){continue}
   $local=Join-Path $stage ($entry.FullName.Replace('/',[IO.Path]::DirectorySeparatorChar))
   $stream=$entry.Open();$sha=[Security.Cryptography.SHA256]::Create()
   try{$hash=[BitConverter]::ToString($sha.ComputeHash($stream)).Replace('-','')}finally{$stream.Dispose();$sha.Dispose()}
   if($hash -ne (Get-FileHash -LiteralPath $local -Algorithm SHA256).Hash){throw "ZIP member hash mismatch: $($entry.FullName)"}
   $count++
  }
  if($count -ne @(Get-ChildItem -LiteralPath $stage -Recurse -File).Count){throw 'ZIP file count mismatch'}
 }finally{$z.Dispose()}
 Move-Item -LiteralPath $zipTemp -Destination $output
 & (Join-Path $PSScriptRoot 'prune-packages.ps1') -DistPath $dist -Keep 3
 Write-Output "Package: $output"
 Write-Output "Version: $version"
 Write-Output "Verified files: $count"
 Write-Output "SHA256: $((Get-FileHash -LiteralPath $output -Algorithm SHA256).Hash)"
 Write-Output "Bytes: $((Get-Item -LiteralPath $output).Length)"
}finally{
 if(Test-Path -LiteralPath $zipTemp){Remove-Item -LiteralPath $zipTemp}
 $resolved=[IO.Path]::GetFullPath($stage)
 $boundary=[IO.Path]::GetFullPath($tempRoot).TrimEnd('\')+'\'
 if(-not $resolved.StartsWith($boundary,[StringComparison]::OrdinalIgnoreCase)){throw 'Unexpected packaging temp path'}
 if(Test-Path -LiteralPath $resolved){Remove-Item -LiteralPath $resolved -Recurse -Force}
}
