$ErrorActionPreference = "Stop"
$repo = "AbhaySingh002/Supremo"
$release = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
$version = $release.tag_name
$asset = "supremo_${version}_windows_amd64.zip"
$checksum = ($release.assets | Where-Object name -eq "checksums.txt").browser_download_url
$archive = ($release.assets | Where-Object name -eq $asset).browser_download_url
if (-not $archive -or -not $checksum) { throw "Release $version does not contain a Windows archive." }

$temp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid()))
try {
  Invoke-WebRequest $archive -OutFile (Join-Path $temp $asset)
  Invoke-WebRequest $checksum -OutFile (Join-Path $temp "checksums.txt")
  $expected = ((Get-Content (Join-Path $temp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($asset) }) -split '\s+')[0]
  if ((Get-FileHash (Join-Path $temp $asset) -Algorithm SHA256).Hash.ToLower() -ne $expected.ToLower()) { throw "Checksum verification failed." }
  $destination = if ($env:SUPREMO_INSTALL_DIR) { $env:SUPREMO_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }
  New-Item -ItemType Directory -Force -Path $destination | Out-Null
  Expand-Archive (Join-Path $temp $asset) -DestinationPath $temp -Force
  Copy-Item (Join-Path $temp "supremo.exe") (Join-Path $destination "supremo.exe") -Force
  Write-Output "Installed Supremo to $destination\supremo.exe"
} finally {
  Remove-Item $temp -Recurse -Force
}
