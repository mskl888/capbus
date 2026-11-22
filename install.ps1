#Requires -Version 5.1
<#
.SYNOPSIS
  Installs capbus on Windows.

.DESCRIPTION
  Prefers a prebuilt release binary. Falls back to building from source when
  no release asset exists yet, which is the state of this project today.

  -Dest lets you install somewhere other than the default, which is also how
  the installer is tested without touching a real installation.
#>
[CmdletBinding()]
param(
    [string]$Dest = (Join-Path $env:LOCALAPPDATA 'capbus'),
    [switch]$NoPath,
    [switch]$NoShortcuts
)

$ErrorActionPreference = 'Stop'
$repo = 'mskl888/capbus'

function Say($m) { Write-Host "  $m" }
function Ok($m)  { Write-Host "  [ok] $m" -ForegroundColor Green }
function Warn($m){ Write-Host "  [!]  $m" -ForegroundColor Yellow }

Write-Host ""
Write-Host "capbus installer" -ForegroundColor Cyan
Write-Host "----------------"

$arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else { '386' }
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$exe = Join-Path $Dest 'capbus.exe'

# 1. A published binary is the fast path. It is absent until a release exists,
#    so a failure here is expected, not an error worth stopping for.
$got = $false
$url = "https://github.com/$repo/releases/latest/download/capbus-windows-$arch.exe"
try {
    Say "looking for a release build..."
    Invoke-WebRequest -Uri $url -OutFile $exe -TimeoutSec 60 -UseBasicParsing
    $got = $true
    Ok "downloaded $url"
} catch {
    Warn "no release build available yet"
}

# 2. Otherwise build it. Go is the only prerequisite, and the message says how
#    to get it rather than failing with a compiler error.
if (-not $got) {
    if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
        Write-Host ""
        Write-Host "needs Go to build from source:" -ForegroundColor Red
        Say "winget install GoLang.Go"
        Say "then open a new terminal and run this installer again"
        exit 1
    }
    Say "building from source (Go $(go version | ForEach-Object { $_.Split(' ')[2] }))..."
    $tmp = Join-Path $env:TEMP ("capbus-src-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null
    $zip = "$tmp.zip"
    Invoke-WebRequest -Uri "https://codeload.github.com/$repo/zip/refs/heads/main" -OutFile $zip -TimeoutSec 120 -UseBasicParsing
    Expand-Archive -Path $zip -DestinationPath $tmp -Force
    $src = (Get-ChildItem $tmp -Directory | Select-Object -First 1).FullName
    Push-Location $src
    $env:GOFLAGS = '-mod=mod'
    if ($env:GOPROXY -eq $null -or $env:GOPROXY -eq '') { $env:GOPROXY = 'https://proxy.golang.org,direct' }
    & go build -o $exe ./cmd/capbus
    Pop-Location
    Remove-Item $tmp, $zip -Recurse -Force -ErrorAction SilentlyContinue
    Ok "built $exe"
}

if (-not (Test-Path $exe)) {
    Write-Host "  [x] install failed: $exe not found" -ForegroundColor Red
    exit 1
}
$version = (& $exe version) 2>&1
Ok $version

# 3. PATH, so `capbus` works in any new terminal.
if (-not $NoPath) {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath -notlike "*$Dest*") {
        $new = if ([string]::IsNullOrWhiteSpace($userPath)) { $Dest } else { "$userPath;$Dest" }
        [Environment]::SetEnvironmentVariable('Path', $new, 'User')
        Ok "added $Dest to your PATH (open a new terminal to use it)"
    } else {
        Ok "PATH already contains $Dest"
    }
}

# 4. Double-click entries. A non-technical user should not have to type a
#    command to see what is wrong with their machine.
if (-not $NoShortcuts) {
    $desktop = [Environment]::GetFolderPath('Desktop')
    $entries = @(
        @{ name = '1-capbus-scan.cmd';     args = 'scan' },
        @{ name = '2-capbus-list.cmd';     args = 'list' },
        @{ name = '3-capbus-cost.cmd';     args = 'cost' },
        @{ name = '4-capbus-sync.cmd';     args = 'sync' },
        @{ name = '5-capbus-activity.cmd'; args = 'activity' },
        @{ name = '6-capbus-undo.cmd';     args = 'undo' },
        @{ name = '7-capbus-map.cmd';      args = 'ui --port 45871' }
    )
    foreach ($e in $entries) {
        $body = "@echo off`r`nchcp 65001 >nul`r`nset CAPBUS_LANG=zh`r`ntitle capbus`r`n`"$exe`" $($e.args)`r`necho.`r`npause`r`n"
        [IO.File]::WriteAllText((Join-Path $desktop $e.name), $body, (New-Object System.Text.ASCIIEncoding))
    }
    Ok "created $($entries.Count) launchers on your Desktop"
}

Write-Host ""
Write-Host "done. next:" -ForegroundColor Cyan
Say "capbus init        # tells you exactly what is left to do"
Write-Host ""
