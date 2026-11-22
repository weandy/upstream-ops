<#
.SYNOPSIS
    UpstreamOps 本地开发与调试辅助脚本（Windows / PowerShell）。
.DESCRIPTION
    严格遵守 AGENTS.md 约定：
    - 编译和测试统一设置 GOTOOLCHAIN=go1.23.0 与 CGO_ENABLED=0
    - 启动后端时自动从 config.yaml 读取 security.appSecret 写入 APP_SECRET（绝不打印）
    - 支持一键编译、重启后端（自动结束占用 8418 的旧进程）、查看状态、接口探活与全量测试
.EXAMPLE
    .\scripts\dev-debug.ps1 -Action status
    .\scripts\dev-debug.ps1 -Action restart
    .\scripts\dev-debug.ps1 -Action diag
    .\scripts\dev-debug.ps1 -Action test
    .\scripts\dev-debug.ps1 -Action verify
#>
[CmdletBinding()]
param(
    [ValidateSet("build", "start", "stop", "restart", "status", "diag", "test", "verify")]
    [string]$Action = "status",

    [int]$BackendPort = 8418,
    [int]$FrontendPort = 3010,
    [string]$EmbedSite = "default",
    [string]$GoTestTargets = "./backend/intelligence/... ./backend/embed/... ./backend/runtimeconfig/..."
)

$ErrorActionPreference = "Stop"
$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
$BinaryPath = Join-Path $RepoRoot "upstream-ops.exe"
$ConfigPath = Join-Path $RepoRoot "config.yaml"
$LogOut = Join-Path $RepoRoot "data\dev-backend.out.log"
$LogErr = Join-Path $RepoRoot "data\dev-backend.err.log"

function Set-GoBuildEnv {
    $env:GOTOOLCHAIN = "go1.23.0"
    $env:CGO_ENABLED = "0"
}

function Load-AppSecretSilently {
    if ($env:APP_SECRET) {
        Write-Host "[secret] 使用当前环境中的 APP_SECRET（已脱敏）" -ForegroundColor DarkGray
        return
    }
    if (Test-Path $ConfigPath) {
        $match = Select-String -Path $ConfigPath -Pattern '^\s*appSecret\s*:\s*(.+)$' | Select-Object -First 1
        if ($match) {
            $raw = $match.Matches[0].Groups[1].Value.Trim().Trim('"').Trim("'")
            if ($raw -ne "") {
                $env:APP_SECRET = $raw
                Write-Host "[secret] 已从 config.yaml 读取 APP_SECRET（已脱敏）" -ForegroundColor DarkGray
                return
            }
        }
    }
    $bytes = New-Object byte[] 32
    [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($bytes)
    $env:APP_SECRET = ([System.BitConverter]::ToString($bytes) -replace '-', '').ToLowerInvariant()
    Write-Host "[secret] config.yaml 未配置 appSecret，已生成临时本地 APP_SECRET（已脱敏）" -ForegroundColor Yellow
}

function Get-ListeningPIDs([int]$Port) {
    $conns = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if (-not $conns) { return @() }
    return ($conns | Select-Object -ExpandProperty OwningProcess -Unique)
}

function Stop-Backend {
    $pids = Get-ListeningPIDs -Port $BackendPort
    if ($pids.Count -eq 0) {
        Write-Host "[stop] 端口 $BackendPort 当前无监听进程" -ForegroundColor DarkGray
        return
    }
    foreach ($p in $pids) {
        $proc = Get-Process -Id $p -ErrorAction SilentlyContinue
        $name = if ($proc) { $proc.ProcessName } else { "unknown" }
        Write-Host "[stop] 结束监听端口 $BackendPort 的进程 PID=$p ($name)" -ForegroundColor Yellow
        Stop-Process -Id $p -Force -ErrorAction SilentlyContinue
    }
    Start-Sleep -Milliseconds 600
}

function Build-Backend {
    Stop-Backend
    Set-GoBuildEnv
    Push-Location $RepoRoot
    try {
        Write-Host "[build] 开始编译 upstream-ops.exe (GOTOOLCHAIN=$env:GOTOOLCHAIN, CGO_ENABLED=$env:CGO_ENABLED)..." -ForegroundColor Cyan
        & go build -o $BinaryPath ./cmd/server
        if ($LASTEXITCODE -ne 0) {
            throw "go build 失败，退出码 $LASTEXITCODE"
        }
        Write-Host "[build] 编译成功：$BinaryPath" -ForegroundColor Green
    } finally {
        Pop-Location
    }
}

function Start-Backend {
    $existing = Get-ListeningPIDs -Port $BackendPort
    if ($existing.Count -gt 0) {
        Write-Host "[start] 端口 $BackendPort 已被进程 $($existing -join ',') 监听，跳过重复启动" -ForegroundColor Yellow
        return
    }
    if (-not (Test-Path $BinaryPath)) {
        Build-Backend
    }
    $dataDir = Join-Path $RepoRoot "data"
    if (-not (Test-Path $dataDir)) {
        New-Item -ItemType Directory -Path $dataDir -Force | Out-Null
    }
    Load-AppSecretSilently

    Write-Host "[start] 启动后端 $BinaryPath ..." -ForegroundColor Cyan
    $proc = Start-Process -FilePath $BinaryPath `
        -WorkingDirectory $RepoRoot `
        -RedirectStandardOutput $LogOut `
        -RedirectStandardError $LogErr `
        -WindowStyle Hidden `
        -PassThru

    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        Start-Sleep -Milliseconds 300
        if ($proc.HasExited) {
            Write-Host "[start] 进程提前退出，退出码=$($proc.ExitCode)" -ForegroundColor Red
            if (Test-Path $LogErr) { Get-Content $LogErr -Tail 30 }
            if (Test-Path $LogOut) { Get-Content $LogOut -Tail 30 }
            throw "后端启动失败"
        }
        try {
            $resp = Invoke-WebRequest -Uri "http://localhost:$BackendPort/healthz" -UseBasicParsing -TimeoutSec 2
            if ($resp.StatusCode -eq 200) {
                $ready = $true
                break
            }
        } catch {
            # 等待端口就绪
        }
    }
    if (-not $ready) {
        throw "后端在超时时间内未通过 /healthz 探活"
    }
    $listening = $false
    foreach ($f in @($LogOut, $LogErr)) {
        if ((Test-Path $f) -and (Select-String -Path $f -Pattern "http server listening" -Quiet)) {
            $listening = $true
        }
    }
    Write-Host "[start] 后端就绪 PID=$($proc.Id) | http://localhost:$BackendPort | listening日志=$listening" -ForegroundColor Green
}

function Show-Status {
    $bPids = Get-ListeningPIDs -Port $BackendPort
    $fPids = Get-ListeningPIDs -Port $FrontendPort
    Write-Host "=== 本地服务状态 ===" -ForegroundColor Cyan
    Write-Host ("后端 ($BackendPort): " + ($(if ($bPids.Count -gt 0) { "运行中 PID=$($bPids -join ',')" } else { "未运行" })))
    Write-Host ("前端 ($FrontendPort): " + ($(if ($fPids.Count -gt 0) { "运行中 PID=$($fPids -join ',')" } else { "未运行" })))
    Write-Host ("config.yaml: " + (Test-Path $ConfigPath))
    Write-Host ("data/db:     " + (Test-Path (Join-Path $RepoRoot "data\upstream-ops.db")))
}

function Run-Diag {
    Show-Status
    Write-Host "`n=== 接口与响应头诊断 ===" -ForegroundColor Cyan
    $urls = @(
        "http://localhost:$BackendPort/healthz",
        "http://localhost:$BackendPort/api/auth/me",
        "http://localhost:$BackendPort/embed?site=$EmbedSite",
        "http://localhost:$BackendPort/api/public/status?site=$EmbedSite",
        "http://localhost:$FrontendPort/",
        "http://localhost:$FrontendPort/api/intelligence/detectors"
    )
    foreach ($u in $urls) {
        try {
            $sw = [System.Diagnostics.Stopwatch]::StartNew()
            $r = Invoke-WebRequest -Uri $u -UseBasicParsing -TimeoutSec 5
            $sw.Stop()
            $cacheHdr = $r.Headers["X-Embed-Cache"]
            $cspHdr = if ($r.Headers["Content-Security-Policy"]) { "CSP=present" } else { "" }
            $extra = @($cacheHdr, $cspHdr) | Where-Object { $_ -and $_.Trim() -ne "" }
            $extraStr = if ($extra.Count -gt 0) { " | " + ($extra -join " | ") } else { "" }
            Write-Host ("[OK {0}] {1,4}ms  {2}{3}" -f $r.StatusCode, $sw.ElapsedMilliseconds, $u, $extraStr) -ForegroundColor Green
        } catch {
            Write-Host ("[ERR]         {0} => {1}" -f $u, $_.Exception.Message) -ForegroundColor Red
        }
    }
}

function Run-Tests {
    Set-GoBuildEnv
    Push-Location $RepoRoot
    try {
        Write-Host "[test] 运行 Go 测试: $GoTestTargets" -ForegroundColor Cyan
        $pkgs = $GoTestTargets -split '\s+' | Where-Object { $_ -ne "" }
        & go test @pkgs
        if ($LASTEXITCODE -ne 0) { throw "Go 测试失败" }
    } finally {
        Pop-Location
    }
}

function Run-Verify {
    Set-GoBuildEnv
    Push-Location $RepoRoot
    try {
        Write-Host "[verify] 1/3 Go 全量测试..." -ForegroundColor Cyan
        & go test ./...
        if ($LASTEXITCODE -ne 0) { throw "Go 全量测试失败" }
    } finally {
        Pop-Location
    }

    Push-Location (Join-Path $RepoRoot "frontend")
    try {
        Write-Host "[verify] 2/3 前端 TypeScript 类型检查..." -ForegroundColor Cyan
        & pnpm exec tsc --noEmit --incremental false
        if ($LASTEXITCODE -ne 0) { throw "tsc 失败" }

        Write-Host "[verify] 3/3 前端 ESLint 检查..." -ForegroundColor Cyan
        & pnpm lint
        if ($LASTEXITCODE -ne 0) { throw "eslint 失败" }
    } finally {
        Pop-Location
    }
    Write-Host "[verify] 全部校验通过" -ForegroundColor Green
}

switch ($Action) {
    "build"   { Build-Backend }
    "start"   { Start-Backend }
    "stop"    { Stop-Backend }
    "restart" { Build-Backend; Start-Backend }
    "status"  { Show-Status }
    "diag"    { Run-Diag }
    "test"    { Run-Tests }
    "verify"  { Run-Verify }
}
