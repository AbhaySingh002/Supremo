$ErrorActionPreference = "Stop"

function Step([string]$Message) {
  Write-Host "==> $Message" -ForegroundColor Green
}

function Success([string]$Message) {
  Write-Host "==> $Message" -ForegroundColor DarkGreen
}

function Warn([string]$Message) {
  Write-Host "==> $Message" -ForegroundColor Red
}

function Fail([string]$Message) {
  Warn $Message
  throw $Message
}

$owner = "AbhaySingh002"
$repo = "supremo"

if ([Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
  Fail "Unsupported operating system (supported: Windows)"
}

Step "Fetching latest version from VERSION file"
$version = (Invoke-RestMethod "https://raw.githubusercontent.com/$owner/$repo/main/VERSION").Trim()
if ([string]::IsNullOrWhiteSpace($version)) {
  Fail "Resolved version is empty."
}

Step "Detecting Windows architecture"
$arch = switch -Regex ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64"; break }
  "ARM64" { "arm64"; break }
  default { "" }
}
if ([string]::IsNullOrEmpty($arch)) {
  Fail "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (supported: amd64, arm64)"
}

$asset = "supremo_${version}_windows_${arch}.zip"
$base = "https://github.com/$owner/$repo/releases/download/$version"
$checksum = "$base/checksums.txt"
$archive = "$base/$asset"

if (-not (Get-Command curl.exe -ErrorAction SilentlyContinue)) {
  Fail "curl.exe is required to install supremo"
}

$temp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid()))
try {
  Step "Downloading $asset"
  & curl.exe -fL -# $archive -o (Join-Path $temp $asset)
  if ($LASTEXITCODE -ne 0) {
    Fail "Download failed for $asset"
  }

  Step "Downloading checksums.txt"
  Invoke-WebRequest $checksum -OutFile (Join-Path $temp "checksums.txt")

  Step "Verifying checksum"
  $expected = ((Get-Content (Join-Path $temp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($asset) }) -split '\s+')[0]
  if ([string]::IsNullOrWhiteSpace($expected)) {
    Fail "Checksum entry not found for $asset"
  }
  if ((Get-FileHash (Join-Path $temp $asset) -Algorithm SHA256).Hash.ToLower() -ne $expected.ToLower()) {
    Fail "Checksum verification failed."
  }

  $destination = Join-Path $HOME ".local\bin"
  Step "Installing to $destination"
  New-Item -ItemType Directory -Force -Path $destination | Out-Null
  Expand-Archive (Join-Path $temp $asset) -DestinationPath $temp -Force
  Copy-Item (Join-Path $temp "supremo.exe") (Join-Path $destination "supremo.exe") -Force

  Success "Installed Supremo to $destination\supremo.exe"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $userPathEntries = @($userPath -split ';' | Where-Object { $_ })
  if ($userPathEntries -notcontains $destination) {
    [Environment]::SetEnvironmentVariable("Path", (($userPathEntries + $destination) -join ';'), "User")
    Success "Added $destination to your User PATH"
  }
  if (($env:Path -split ';') -notcontains $destination) {
    $env:Path = "$destination;$env:Path"
  }
  Success "Run: supremo --version"
} finally {
  Remove-Item $temp -Recurse -Force
}
