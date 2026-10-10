[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet("controller", "worker")][string]$Mode,
    [Parameter(Mandatory = $true)][string]$Config,
    [Parameter(Mandatory = $true)][string]$Executable
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$taskConfigPath = (Resolve-Path -LiteralPath $Config).Path
$taskExecutablePath = (Resolve-Path -LiteralPath $Executable).Path
$taskWorkingDirectory = Split-Path -Parent $taskConfigPath
$taskLogDirectory = Join-Path $taskWorkingDirectory "logs"
if (-not (Test-Path -LiteralPath $taskLogDirectory -PathType Container)) {
    New-Item -ItemType Directory -Path $taskLogDirectory | Out-Null
}
$taskProcess = Start-Process `
    -FilePath $taskExecutablePath `
    -ArgumentList @($Mode, "--config", ('"' + $taskConfigPath + '"')) `
    -WorkingDirectory $taskWorkingDirectory `
    -WindowStyle Hidden `
    -RedirectStandardOutput (Join-Path $taskLogDirectory ("codex-" + $Mode + ".stdout.log")) `
    -RedirectStandardError (Join-Path $taskLogDirectory ("codex-" + $Mode + ".stderr.log")) `
    -Wait `
    -PassThru
exit $taskProcess.ExitCode
