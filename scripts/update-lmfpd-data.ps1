param(
    [string]$OutDir = "./data/lmfpd",
    [string]$BaseUrl = "https://raw.githubusercontent.com/haowang02/cpa-plugin-codex-candy-eval/main/internal/fingerprint/data"
)

$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

$files = @("shared_detector.bin", "models.json", "enrollment-suite.json", "LICENSE")
foreach ($f in $files) {
    $src = "$BaseUrl/$f"
    $dst = Join-Path $OutDir $f
    Write-Host "Downloading $src -> $dst"
    Invoke-WebRequest -Uri $src -OutFile $dst
}
Write-Host "Done. External lmfpd reference bank updated at $OutDir"
