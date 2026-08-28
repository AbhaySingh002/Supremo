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

function Download-Asset([string]$Url, [string]$OutPath, [string]$DisplayName) {
  Step "Downloading $DisplayName..."
  $downloaded = $false

  try {
    $client = [System.Net.Http.HttpClient]::new()
    $client.Timeout = [System.TimeSpan]::FromSeconds(30)
    $response = $client.GetAsync($Url, [System.Net.Http.HttpCompletionOption]::ResponseHeadersRead).GetAwaiter().GetResult()
    if ($response.IsSuccessStatusCode) {
      $totalBytes = $response.Content.Headers.ContentLength
      $stream = $response.Content.ReadAsStreamAsync().GetAwaiter().GetResult()
      $fs = [System.IO.File]::Create($OutPath)
      $buffer = New-Object byte[] 32768
      $received = 0
      $lastDraw = [DateTime]::MinValue
      $barWidth = 24

      while (($read = $stream.Read($buffer, 0, $buffer.Length)) -gt 0) {
        $fs.Write($buffer, 0, $read)
        $received += $read
        $now = [DateTime]::UtcNow
        if (($now - $lastDraw).TotalMilliseconds -ge 60) {
          $lastDraw = $now
          if ($totalBytes -and $totalBytes -gt 0) {
            $pct = [Math]::Min(100, [int](($received / $totalBytes) * 100))
            $filled = [Math]::Min($barWidth, [int](($received / $totalBytes) * $barWidth))
            $empty = $barWidth - $filled
            $barFilled = "█" * $filled
            $barEmpty = "░" * $empty
            $curMB = ($received / 1MB).ToString("0.0")
            $totMB = ($totalBytes / 1MB).ToString("0.0")
            Write-Host -NoNewline "`r    "
            Write-Host -NoNewline $barFilled -ForegroundColor Yellow
            Write-Host -NoNewline $barEmpty -ForegroundColor DarkGray
            Write-Host -NoNewline " $($pct.ToString().PadLeft(3))% " -ForegroundColor White
            Write-Host -NoNewline "($curMB MB / $totMB MB)  " -ForegroundColor DarkGray
          } else {
            $curMB = ($received / 1MB).ToString("0.0")
            Write-Host -NoNewline "`r    "
            Write-Host -NoNewline "▰▰▰▱▱▱ " -ForegroundColor Yellow
            Write-Host -NoNewline "$curMB MB downloaded...  " -ForegroundColor DarkGray
          }
        }
      }
      $fs.Flush()
      $fs.Close()
      $stream.Close()
      $client.Dispose()
      Write-Host "`r                                                                     `r" -NoNewline
      $downloaded = $true
    }
  } catch {
    # If custom streaming fails, fallback below
  }

  if (-not $downloaded) {
    if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
      & curl.exe -# -fL --connect-timeout 15 $Url -o $OutPath
      if ($LASTEXITCODE -ne 0) {
        Fail "Download failed for $DisplayName"
      }
    } else {
      Invoke-WebRequest $Url -OutFile $OutPath -UseBasicParsing -TimeoutSec 30
    }
  }
  Success "Downloaded $DisplayName"
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
Info "Resolving latest version..."
$version = (Invoke-RestMethod "https://raw.githubusercontent.com/$owner/$repo/main/VERSION" -TimeoutSec 10).Trim()
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
  Download-Asset -Url $archive -OutPath (Join-Path $temp $asset) -DisplayName $asset

  Info "Fetching checksums..."
  if (Get-Command curl.exe -ErrorAction SilentlyContinue) {
    & curl.exe -fsSL --connect-timeout 10 $checksum -o (Join-Path $temp "checksums.txt")
  } else {
    Invoke-WebRequest $checksum -OutFile (Join-Path $temp "checksums.txt") -UseBasicParsing -TimeoutSec 10
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
