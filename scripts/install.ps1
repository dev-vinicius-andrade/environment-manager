<#
.SYNOPSIS
  Install envmgr from the latest GitHub release (Windows).
.DESCRIPTION
  irm https://raw.githubusercontent.com/dev-vinicius-andrade/environment-manager/main/scripts/install.ps1 | iex

  Environment:
    ENVMGR_VERSION      version to install, e.g. 1.2.0 (default: latest release)
    ENVMGR_INSTALL_DIR  where to put envmgr.exe (default: %LOCALAPPDATA%\envmgr\bin)
    GITHUB_TOKEN        optional, avoids the GitHub anonymous rate limit

  The archive SHA-256 is checked against the release checksums.txt before
  anything is installed. The install directory is added to the user PATH (not
  the machine PATH); no administrator rights are needed.
#>
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$repo = 'dev-vinicius-andrade/environment-manager'
function Say($m) { Write-Host "envmgr-install: $m" }
function Fail($m) { throw "envmgr-install: $m" }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { 'amd64' }
  'ARM64' { 'arm64' }
  default { Fail "unsupported architecture $($env:PROCESSOR_ARCHITECTURE)" }
}

$version = $env:ENVMGR_VERSION
if (-not $version) {
  Say 'looking up the latest release'
  $headers = @{ 'User-Agent' = 'envmgr-install' }
  if ($env:GITHUB_TOKEN) { $headers['Authorization'] = "Bearer $($env:GITHUB_TOKEN)" }
  try { $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest" -Headers $headers }
  catch { Fail 'could not find a release (has one been published?)' }
  $version = $rel.tag_name
}
$version = $version.TrimStart('v')
if ($version -notmatch '^[0-9A-Za-z.+-]+$') { Fail "invalid version '$version'" }

$installDir = if ($env:ENVMGR_INSTALL_DIR) { $env:ENVMGR_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'envmgr\bin' }
$name = "envmgr_${version}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/v$version"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("envmgr-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Say "downloading $name"
  Invoke-WebRequest -Uri "$base/$name" -OutFile (Join-Path $tmp $name) -UseBasicParsing
  Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -replace '^\*', '' -eq $name } | Select-Object -First 1
  if (-not $line) { Fail "no checksum for $name in checksums.txt; refusing to install" }
  $want = ($line -split '\s+')[0].ToLower()
  $got = (Get-FileHash -Algorithm SHA256 (Join-Path $tmp $name)).Hash.ToLower()
  if ($got -ne $want) { Fail "checksum mismatch for $name (expected $want, got $got)" }
  Say 'checksum ok'

  Expand-Archive -Path (Join-Path $tmp $name) -DestinationPath (Join-Path $tmp 'x') -Force
  $exe = Join-Path $tmp 'x\envmgr.exe'
  if (-not (Test-Path $exe)) { Fail 'envmgr.exe not found in the archive' }

  New-Item -ItemType Directory -Path $installDir -Force | Out-Null
  $dest = Join-Path $installDir 'envmgr.exe'
  # A running envmgr.exe cannot be overwritten but can be renamed (the tray may be running).
  if (Test-Path $dest) {
    Remove-Item "$dest.old" -Force -ErrorAction SilentlyContinue
    Rename-Item $dest 'envmgr.exe.old' -Force
  }
  Copy-Item $exe $dest -Force
  Say "installed envmgr $version to $dest"

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $parts = @($userPath -split ';' | Where-Object { $_ })
  if ($parts -notcontains $installDir) {
    [Environment]::SetEnvironmentVariable('Path', (($parts + $installDir) -join ';'), 'User')
    $env:Path = "$env:Path;$installDir"
    Say "added $installDir to your user PATH (open a new terminal for other windows)"
  }
  Say 'next: see the README to install the shell hook; update later with: envmgr update'
}
finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
