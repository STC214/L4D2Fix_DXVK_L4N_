$ErrorActionPreference='Stop'
$project=Split-Path -Parent $PSScriptRoot
$resources=Join-Path $project 'L4N_Go_Win32_Portable/resources'
$catalog=Get-Content -LiteralPath (Join-Path $project 'cmd/l4nfix/resource_catalog.json') -Raw -Encoding UTF8 | ConvertFrom-Json
$mods=Get-Content -LiteralPath (Join-Path $project 'cmd/l4nfix/bundled_skeeto_mods.json') -Raw -Encoding UTF8 | ConvertFrom-Json
foreach($kind in @('l4n','dxvk','skeeto','evolved')) {
 $expected=@{l4n=2;dxvk=4;skeeto=1;evolved=1}[$kind]
 if(@($catalog.bundled | Where-Object kind -eq $kind).Count -ne $expected){throw "Wrong offline version count: $kind"}
}
$rows=@($catalog.bundled)+@($mods.mods | ForEach-Object {[pscustomobject]@{archive=$_.archive;size=$_.detail.file_size;sha256=$_.sha256}})
$boundary=[IO.Path]::GetFullPath($resources).TrimEnd('\')+'\'
foreach($row in $rows) {
 $path=[IO.Path]::GetFullPath((Join-Path $resources $row.archive))
 if(-not $path.StartsWith($boundary,[StringComparison]::OrdinalIgnoreCase)){throw 'Offline resource escaped root'}
 $cursor=$path
 while($cursor -and $cursor.Length -ge $boundary.TrimEnd('\').Length) {
  if((Get-Item -LiteralPath $cursor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Offline resources reject links'}
  $cursor=[IO.Path]::GetDirectoryName($cursor)
 }
 if((Get-Item -LiteralPath $path).Length -ne [long]$row.size -or (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash -ne $row.sha256){throw "Offline resource hash/length mismatch: $($row.archive)"}
}
Write-Output "Verified offline payloads: $($rows.Count) (2 L4N, 4 DXVK, 1 Skeeto, 1 EVO, 2 Skeeto MODs)"
