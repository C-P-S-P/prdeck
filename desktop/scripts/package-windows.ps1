# Packs PR Deck for Windows into desktop\dist\prdeck-desktop-<version>-windows-<arch>.zip:
# "PR Deck.exe" and prdeck.exe, the Go core it starts, side by side.
# Usage (on Windows): powershell -ExecutionPolicy Bypass -File desktop\scripts\package-windows.ps1
$ErrorActionPreference = 'Stop'
$here = (Resolve-Path "$PSScriptRoot\..").Path
$root = (Resolve-Path "$here\..").Path

cargo build --release --locked --manifest-path "$here\Cargo.toml"
if ($LASTEXITCODE) { exit $LASTEXITCODE }
$version = (Select-String -Path "$here\Cargo.toml" -Pattern '^version = "(.+)"' | Select-Object -First 1).Matches[0].Groups[1].Value
$name = "prdeck-desktop-$version-windows-$($env:PROCESSOR_ARCHITECTURE.ToLower())"
$out = "$here\dist\$name"
if (Test-Path $out) { Remove-Item -Recurse -Force $out }
New-Item -ItemType Directory -Force $out | Out-Null
Copy-Item "$here\target\release\prdeck-desktop.exe" "$out\PR Deck.exe"
Push-Location $root
try { go build -o "$out\prdeck.exe" ./cmd/prdeck } finally { Pop-Location }
if ($LASTEXITCODE) { exit $LASTEXITCODE }

Set-Content -Encoding utf8 -Path "$out\README.txt" -Value @"
PR Deck $version for Windows

Unzip anywhere and run "PR Deck.exe". Keep prdeck.exe next to it: it is the core
the app starts, and the terminal app too (add this folder to PATH to use it there).

Needs Git for Windows (https://gitforwindows.org), whose sh runs PR Deck's commands,
and the GitHub CLI; PR Deck's first-run setup installs gh with winget when it is
missing.

https://github.com/MasterPushers/prdeck
"@

$zip = "$here\dist\$name.zip"
if (Test-Path $zip) { Remove-Item -Force $zip }
Compress-Archive -Path $out -DestinationPath $zip
Write-Host "Built $zip"
