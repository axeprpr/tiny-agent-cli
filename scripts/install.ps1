$ErrorActionPreference = "Stop"

$Repo = "axeprpr/onek-agent"
$BinName = "onek.exe"
$Version = if ($env:ONEK_VERSION) { $env:ONEK_VERSION } else { "latest" }
$InstallDir = if ($env:ONEK_INSTALL_DIR) { $env:ONEK_INSTALL_DIR } else { Join-Path $HOME ".local\bin" }

$arch = switch -Regex ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLower()) {
    "x64" { "amd64"; break }
    "arm64" { "arm64"; break }
    default { throw "unsupported architecture: $($_)" }
}

$asset = "onek-windows-$arch.exe"
$base = "https://github.com/$Repo/releases"
if ($Version -eq "latest") {
    $url = "$base/latest/download/$asset"
} else {
    $url = "$base/download/$Version/$asset"
}

New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
$target = Join-Path $InstallDir $BinName

Write-Host "downloading $url"
Invoke-WebRequest $url -OutFile $target
Write-Host "installed $BinName to $target"
Write-Host "run: $target version"
