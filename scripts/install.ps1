$ErrorActionPreference = "Stop"
$repo = "AbhaySingh002/Supremo"
$version = (Invoke-RestMethod "https://raw.githubusercontent.com/$repo/main/VERSION").Trim()
$asset = "supremo_${version}_windows_amd64.zip"
$base = "https://github.com/$repo/releases/download/$version"
$checksum = "$base/checksums.txt"
$archive = "$base/$asset"

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

  # Add to PATH automatically
  function Setup-Path {
    $currentPath = [Environment]::GetEnvironmentVariable("Path", "User")
    
    # Check if already in PATH
    if ($currentPath -like "*$destination*") {
      Write-Output "PATH already configured. You can run: supremo"
      return
    }

    Write-Output "Adding $destination to PATH..."
    
    # Add to user PATH
    [Environment]::SetEnvironmentVariable("Path", "$currentPath;$destination", "User")
    
    # Update current session
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "User") + ";" + [Environment]::GetEnvironmentVariable("Path", "Machine")
    
    Write-Output "PATH configured. Restart your terminal or run:"
    Write-Output "  `$env:Path = `"$destination;`$env:Path`""
    Write-Output "Then run: supremo"
  }

  Setup-Path
} finally {
  Remove-Item $temp -Recurse -Force
}
