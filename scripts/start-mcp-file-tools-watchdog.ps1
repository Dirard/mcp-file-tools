$ErrorActionPreference = "Stop"

$Root = Resolve-Path (Join-Path $PSScriptRoot "..")
$Exe = Join-Path $Root "mcp-file-tools.exe"
$LogDir = Join-Path $Root "logs"
$ServerLog = Join-Path $LogDir "mcp-file-tools.log"
$WatchdogLog = Join-Path $LogDir "watchdog.log"
$HttpAddr = if ($env:MCP_HTTP_ADDR) { $env:MCP_HTTP_ADDR } else { "127.0.0.1:8787" }

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

function Write-WatchdogLog {
    param([string]$Message)

    $timestamp = Get-Date -Format o
    Add-Content -LiteralPath $WatchdogLog -Encoding UTF8 -Value "$timestamp $Message"
}

Write-WatchdogLog "watchdog started root=$Root exe=$Exe http=$HttpAddr"

while ($true) {
    if (-not (Test-Path -LiteralPath $Exe)) {
        Write-WatchdogLog "executable not found; retrying in 10s"
        Start-Sleep -Seconds 10
        continue
    }

    Write-WatchdogLog "starting mcp-file-tools"
    try {
        $arguments = '--http "{0}" --log-file "{1}"' -f ($HttpAddr -replace '"', '\"'), ($ServerLog -replace '"', '\"')
        $process = Start-Process -FilePath $Exe -ArgumentList $arguments -WindowStyle Hidden -PassThru -Wait
        $exitCode = $process.ExitCode
    } catch {
        $exitCode = "start_failed"
        Write-WatchdogLog "failed to start mcp-file-tools error=$($_.Exception.Message)"
    }
    Write-WatchdogLog "mcp-file-tools exited exit_code=$exitCode; restarting in 3s"
    Start-Sleep -Seconds 3
}
