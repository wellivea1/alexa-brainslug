[CmdletBinding()]
param()

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$root = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot "..\.."))
$executable = Join-Path $root "bin\brainslug.exe"
$logDirectory = Join-Path $root "logs"
$stdoutLog = Join-Path $logDirectory "brainslug.stdout.log"
$stderrLog = Join-Path $logDirectory "brainslug.stderr.log"

if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
    throw "Brainslug executable is missing: $executable"
}
if (-not (Test-Path -LiteralPath $logDirectory -PathType Container)) {
    New-Item -ItemType Directory -Path $logDirectory | Out-Null
}

$process = Start-Process `
    -FilePath $executable `
    -WorkingDirectory $root `
    -WindowStyle Hidden `
    -RedirectStandardOutput $stdoutLog `
    -RedirectStandardError $stderrLog `
    -Wait `
    -PassThru

exit $process.ExitCode
