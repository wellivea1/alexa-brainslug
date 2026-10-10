[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][ValidateSet("controller", "worker")][string]$Mode,
    [Parameter(Mandatory = $true)][string]$Config,
    [Parameter(Mandatory = $true)][string]$Executable
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
Import-Module ScheduledTasks -ErrorAction Stop
$taskConfigPath = (Resolve-Path -LiteralPath $Config).Path
$taskExecutablePath = (Resolve-Path -LiteralPath $Executable).Path
$taskRunnerPath = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot "run-brainslug-codex.ps1")).Path
$taskPowerShellPath = Join-Path $env:SystemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
$taskArguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $taskRunnerPath + '" -Mode ' + $Mode + ' -Config "' + $taskConfigPath + '" -Executable "' + $taskExecutablePath + '"'
$taskIdentity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
$taskName = "Brainslug-Codex-" + $Mode
$taskAction = New-ScheduledTaskAction -Execute $taskPowerShellPath -Argument $taskArguments -WorkingDirectory (Split-Path -Parent $taskConfigPath)
$taskTrigger = New-ScheduledTaskTrigger -AtLogOn -User $taskIdentity
$taskPrincipal = New-ScheduledTaskPrincipal -UserId $taskIdentity -LogonType Interactive -RunLevel Limited
$taskSettings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -StartWhenAvailable `
    -RestartCount 3 `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -MultipleInstances IgnoreNew
Register-ScheduledTask -TaskName $taskName -Action $taskAction -Trigger $taskTrigger -Principal $taskPrincipal -Settings $taskSettings -Description ("Run the independently installed Brainslug Codex " + $Mode + " for this user.") -Force | Out-Null
Write-Host "Registered $taskName for $taskIdentity. The Alexa gateway startup task is independent."
