$ErrorActionPreference = "Stop"

function Info([string]$Message) {
  Write-Host "  · " -ForegroundColor DarkGray -NoNewline
  Write-Host $Message -ForegroundColor Gray
}

function Step([string]$Message) {
  Write-Host "  → " -ForegroundColor Yellow -NoNewline
  Write-Host $Message -ForegroundColor White
}

function Success([string]$Message) {
  Write-Host "  ✓ " -ForegroundColor Green -NoNewline
  Write-Host $Message -ForegroundColor White
}

function Warn([string]$Message) {
  Write-Host "  ! " -ForegroundColor Yellow -NoNewline
  Write-Host $Message -ForegroundColor Gray
}

function Fail([string]$Message) {
  Write-Host ""
  Write-Host "  × $Message" -ForegroundColor Red
  Write-Host ""
  throw $Message
}

# Banner
Write-Host ""
Write-Host "  ░█▀▀░█░█░█▀█░█▀▄░█▀▀░█▄█░█▀█" -ForegroundColor Yellow
Write-Host "  ░▀▀█░█░█░█▀▀░█▀▄░█▀▀░█░█░█░█" -ForegroundColor Yellow
Write-Host "  ░▀▀▀░▀▀▀░▀░░░▀░▀░▀▀▀░▀░▀░▀▀▀" -ForegroundColor Yellow
Write-Host ""
Write-Host "  SUPREMO " -ForegroundColor White -NoNewline
Write-Host "· Agentic coding in your local workspace" -ForegroundColor Gray
Write-Host ""

$owner = "AbhaySingh002"
$repo = "supremo"

if ([Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
  Fail "Unsupported operating system (supported: Windows)"
}

# 1. Architecture Detection
$arch = switch -Regex ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { "amd64"; break }
  "ARM64" { "arm64"; break }
  default { "" }
}
if ([string]::IsNullOrEmpty($arch)) {
  Fail "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (supported: amd64, arm64)"
}

Info "Detected platform: windows / $arch"

# 2. Version Resolution
$version = (Invoke-RestMethod "https://raw.githubusercontent.com/$owner/$repo/main/VERSION").Trim()
if ([string]::IsNullOrWhiteSpace($version)) {
  Fail "Resolved version is empty."
}

Info "Latest version: $version"

$asset = "supremo_${version}_windows_${arch}.zip"
$base = "https://github.com/$owner/$repo/releases/download/$version"
$checksum = "$base/checksums.txt"
$archive = "$base/$asset"

$temp = New-Item -ItemType Directory -Path (Join-Path ([IO.Path]::GetTempPath()) ([guid]::NewGuid()))
try {
  Info "Downloading $asset..."
  if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
    & curl.exe -fsSL $archive -o (Join-Path $temp $asset)
    if ($LASTEXITCODE -ne 0) {
      Fail "Download failed for $asset"
    }
    & curl.exe -fsSL $checksum -o (Join-Path $temp "checksums.txt")
  } else {
    Invoke-WebRequest $archive -OutFile (Join-Path $temp $asset) -UseBasicParsing
    Invoke-WebRequest $checksum -OutFile (Join-Path $temp "checksums.txt") -UseBasicParsing
  }

  $expected = ((Get-Content (Join-Path $temp "checksums.txt") | Where-Object { $_ -match [regex]::Escape($asset) }) -split '\s+')[0]
  if ([string]::IsNullOrWhiteSpace($expected)) {
    Fail "Checksum entry not found for $asset"
  }
  if ((Get-FileHash (Join-Path $temp $asset) -Algorithm SHA256).Hash.ToLower() -ne $expected.ToLower()) {
    Fail "Checksum verification failed."
  }
  Success "Checksum verified"

  $destination = Join-Path $HOME ".local\bin"
  New-Item -ItemType Directory -Force -Path $destination | Out-Null
  Expand-Archive (Join-Path $temp $asset) -DestinationPath $temp -Force
  Copy-Item (Join-Path $temp "supremo.exe") (Join-Path $destination "supremo.exe") -Force

  Success "Installed binary to $destination\supremo.exe"

  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  $userPathEntries = @($userPath -split ';' | Where-Object { $_ })
  if ($userPathEntries -notcontains $destination) {
    [Environment]::SetEnvironmentVariable("Path", (($userPathEntries + $destination) -join ';'), "User")
    Info "Added $destination to your User PATH"
  }
  if (($env:Path -split ';') -notcontains $destination) {
    $env:Path = "$destination;$env:Path"
  }

  Write-Host ""
  Write-Host "  ✓ Supremo $version is ready!" -ForegroundColor Green
  Write-Host ""
  Write-Host "  Run:" -ForegroundColor Gray
  Write-Host "    supremo" -ForegroundColor White
  Write-Host ""
} finally {
  Remove-Item $temp -Recurse -Force -ErrorAction SilentlyContinue
}
