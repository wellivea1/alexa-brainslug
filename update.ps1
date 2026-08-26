[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = "Medium")]
param(
    [switch]$InstallGo,
    [switch]$SkipTests,
    [switch]$NoRestart,
    [switch]$NonInteractive,

    [ValidateNotNullOrEmpty()]
    [string]$Branch = "main",

    [ValidateNotNullOrEmpty()]
    [string]$TaskName = "Brainslug",

    [Parameter(DontShow = $true)]
    [string]$GitHubToken = ""
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$root = [IO.Path]::GetFullPath($PSScriptRoot)
$modulePath = Join-Path $root "scripts\windows\Brainslug.Windows.psm1"
Import-Module $modulePath -Force

Write-BrainslugStartBanner -Action "WINDOWS UPDATE"
Assert-BrainslugWindows

$isGitCheckout = Test-Path -LiteralPath (Join-Path $root ".git") -PathType Container
$isArchiveInstall = Test-Path -LiteralPath (Join-Path $root ".brainslug-source.json") -PathType Leaf
$gitCommand = $null
$sourceDescription = ""
if ($isGitCheckout) {
    $gitCommand = Get-Command git.exe -ErrorAction SilentlyContinue
    if ($null -eq $gitCommand) {
        throw "This is a Git checkout, so Git is required to update it."
    }
    $currentBranch = (& $gitCommand.Source -C $root branch --show-current 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Could not read the current Git branch."
    }
    if ($currentBranch -ne $Branch) {
        throw "Update requires branch '$Branch'; the checkout is on '$currentBranch'."
    }
    $dirty = (& $gitCommand.Source -C $root status --porcelain --untracked-files=no 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Could not inspect the Git worktree."
    }
    if ($dirty -ne "") {
        throw "The Git worktree has tracked changes. Commit or stash them before updating."
    }
    $sourceDescription = "origin/$Branch"
}
elseif ($isArchiveInstall) {
    $archiveManifest = Test-BrainslugArchiveSourceClean -Root $root
    $sourceDescription = "https://github.com/$($archiveManifest.Repository)/tree/$($archiveManifest.Branch)"
}
else {
    throw "No Git checkout or archive-install metadata was found. Reinstall with install.ps1."
}

Write-Host "Repository: $root" -ForegroundColor DarkGray
Write-Host "Update source: $sourceDescription" -ForegroundColor DarkGray
if (-not $PSCmdlet.ShouldProcess($root, "Stage source, rebuild, replace the binary, and restart when currently enabled")) {
    Write-Host "Dry run complete. No files, processes, or scheduled tasks were changed." -ForegroundColor Yellow
    return
}

$goExecutable = Get-BrainslugGo `
    -Root $root `
    -InstallWhenMissing $InstallGo.IsPresent `
    -Interactive (-not $NonInteractive.IsPresent)
if ($isGitCheckout) {
    Invoke-BrainslugRetry -Description "Git fetch" -Operation {
        Invoke-BrainslugNative `
            -FilePath $gitCommand.Source `
            -Arguments @("-C", $root, "fetch", "origin", $Branch) `
            -Description "Git fetch"
    } | Out-Null
    & $gitCommand.Source -C $root merge-base --is-ancestor HEAD ("origin/" + $Branch)
    if ($LASTEXITCODE -ne 0) {
        throw "The local branch has commits that are not in origin/$Branch. Refusing a non-fast-forward update."
    }
    Invoke-BrainslugNative `
        -FilePath $gitCommand.Source `
        -Arguments @("-C", $root, "merge", "--ff-only", ("origin/" + $Branch)) `
        -Description "Git fast-forward"
}
else {
    if ($GitHubToken -eq "" -and -not [string]::IsNullOrWhiteSpace($env:BRAINSLUG_GITHUB_TOKEN)) {
        $GitHubToken = $env:BRAINSLUG_GITHUB_TOKEN
    }
    Update-BrainslugArchiveSource -Root $root -GitHubToken $GitHubToken | Out-Null
}

$binDirectory = Join-Path $root "bin"
$candidatePath = Join-Path $binDirectory "brainslug.next.exe"
$executablePath = Join-Path $binDirectory "brainslug.exe"
Invoke-BrainslugBuild `
    -Root $root `
    -GoExecutable $goExecutable `
    -CandidatePath $candidatePath `
    -SkipTests $SkipTests.IsPresent

$task = Get-BrainslugTask -TaskName $TaskName
$taskShouldRun = $null -ne $task -and $task.State -ne "Disabled"
Stop-BrainslugStartupTask -TaskName $TaskName | Out-Null
$processWasRunning = Stop-BrainslugProcess -ExecutablePath $executablePath
$backupPath = Install-BrainslugCandidate -Root $root -CandidatePath $candidatePath -ExecutablePath $executablePath

$shouldRestart = -not $NoRestart -and ($taskShouldRun -or $processWasRunning)
$healthy = $true
if ($shouldRestart) {
    if ($taskShouldRun) {
        Start-BrainslugStartupTask -TaskName $TaskName
    }
    else {
        Start-BrainslugDetached -Root $root | Out-Null
    }
    $healthy = Wait-BrainslugHealth -Root $root
}

if (-not $healthy -and $shouldRestart -and (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
    Write-Warning "The updated process did not become healthy. Restoring the previous binary."
    Stop-BrainslugStartupTask -TaskName $TaskName | Out-Null
    Stop-BrainslugProcess -ExecutablePath $executablePath | Out-Null
    Assert-BrainslugPathInsideRoot -Root $root -Path $executablePath
    Assert-BrainslugPathInsideRoot -Root $root -Path $backupPath
    Remove-Item -LiteralPath $executablePath -Force
    Move-Item -LiteralPath $backupPath -Destination $executablePath
    if ($taskShouldRun) {
        Start-BrainslugStartupTask -TaskName $TaskName
    }
    else {
        Start-BrainslugDetached -Root $root | Out-Null
    }
    Wait-BrainslugHealth -Root $root | Out-Null
    throw "Brainslug update was rolled back because the new process failed its health check."
}

Write-BrainslugSuccessBanner -Message "BRAINSLUG UPDATED"
Write-Host "Source: $sourceDescription"
Write-Host "Executable: $executablePath"
if ($backupPath -and (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
    Write-Host "Rollback binary: $backupPath"
}
if ($shouldRestart) {
    Write-Host "Brainslug was restarted successfully."
}
elseif ($NoRestart) {
    Write-Host "Restart skipped by request."
}
else {
    Write-Host "Brainslug was not running, so the updater left it stopped."
}
