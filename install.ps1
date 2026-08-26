[CmdletBinding(SupportsShouldProcess = $true, ConfirmImpact = "Medium")]
param(
    [ValidateSet("Prompt", "Enable", "Disable")]
    [string]$Startup = "Prompt",

    [switch]$InstallGo,
    [switch]$SkipTests,
    [switch]$NoStart,
    [switch]$NonInteractive,

    [string]$InstallDirectory = "",

    [ValidatePattern("^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$")]
    [string]$Repository = "wellivea1/alexa-brainslug",

    [ValidatePattern("^[A-Za-z0-9._/-]+$")]
    [string]$Branch = "main",

    [Parameter(DontShow = $true)]
    [string]$GitHubToken = "",

    [ValidateNotNullOrEmpty()]
    [string]$TaskName = "Brainslug",

    [Parameter(DontShow = $true)]
    [switch]$BootstrapChild
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

function Write-BrainslugBootstrapBanner {
    Write-Host @'
  ____            _           _
 | __ ) _ __ __ _(_)_ __  ___| |_   _  __ _
 |  _ \| '__/ _` | | '_ \/ __| | | | |/ _` |
 | |_) | | | (_| | | | | \__ \ | |_| | (_| |
 |____/|_|  \__,_|_|_| |_|___/_|\__,_|\__, |
                                        |___/
'@ -ForegroundColor Cyan
    Write-Host "                     WINDOWS INSTALL" -ForegroundColor DarkCyan
    Write-Host ""
}

function Test-BrainslugRepositoryRoot {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) {
        return $false
    }
    $goModule = Join-Path $Path "go.mod"
    $module = Join-Path $Path "scripts\windows\Brainslug.Windows.psm1"
    return (Test-Path -LiteralPath $goModule -PathType Leaf) -and
        (Test-Path -LiteralPath $module -PathType Leaf) -and
        ((Get-Content -LiteralPath $goModule -TotalCount 1) -eq "module github.com/wellivea1/alexa-brainslug")
}

function Assert-BrainslugBootstrapTemporaryPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    $prefix = $temporaryRoot + [IO.Path]::DirectorySeparatorChar + "brainslug-install-"
    if (-not $resolvedPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify an unexpected temporary path: $resolvedPath"
    }
}

function Invoke-BrainslugInstalledInstaller {
    param([Parameter(Mandatory = $true)][string]$Root)

    $childParameters = @{
        Startup = $Startup
        InstallGo = $InstallGo.IsPresent
        SkipTests = $SkipTests.IsPresent
        NoStart = $NoStart.IsPresent
        NonInteractive = $NonInteractive.IsPresent
        InstallDirectory = $Root
        Repository = $Repository
        Branch = $Branch
        GitHubToken = $GitHubToken
        TaskName = $TaskName
        BootstrapChild = $true
    }
    & (Join-Path $Root "install.ps1") @childParameters
}

function Get-BrainslugBootstrapSource {
    param(
        [Parameter(Mandatory = $true)][string]$TemporaryRoot,
        [Parameter(Mandatory = $true)][uri]$ArchiveURI,
        [hashtable]$Headers = @{}
    )

    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $archivePath = Join-Path $TemporaryRoot "source.zip"
    $partialPath = $archivePath + ".partial"
    $extractPath = Join-Path $TemporaryRoot "extract"
    $attempts = 5
    for ($attempt = 1; $attempt -le $attempts; $attempt++) {
        try {
            if (Test-Path -LiteralPath $partialPath -PathType Leaf) {
                Remove-Item -LiteralPath $partialPath -Force
            }
            if (Test-Path -LiteralPath $archivePath -PathType Leaf) {
                Remove-Item -LiteralPath $archivePath -Force
            }
            if (Test-Path -LiteralPath $extractPath -PathType Container) {
                Remove-Item -LiteralPath $extractPath -Recurse -Force
            }
            Write-Host "Downloading Brainslug source (attempt $attempt of $attempts)..." -ForegroundColor Cyan
            Invoke-WebRequest -Uri $ArchiveURI -Headers $Headers -OutFile $partialPath -UseBasicParsing
            Move-Item -LiteralPath $partialPath -Destination $archivePath
            Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
            $moduleFiles = @(Get-ChildItem -LiteralPath $extractPath -Filter go.mod -File -Recurse | Where-Object {
                (Get-Content -LiteralPath $_.FullName -TotalCount 1) -eq "module github.com/wellivea1/alexa-brainslug"
            })
            if ($moduleFiles.Count -ne 1) {
                throw "The source archive did not contain exactly one Brainslug repository."
            }
            return $moduleFiles[0].Directory.FullName
        }
        catch {
            if ($attempt -eq $attempts) {
                throw "Brainslug source download failed after $attempts attempts: $($_.Exception.Message)"
            }
            $delaySeconds = [Math]::Min([Math]::Pow(2, $attempt), 15)
            Write-Warning "Source download was interrupted. Retrying in $delaySeconds seconds."
            Start-Sleep -Seconds $delaySeconds
        }
        finally {
            if (Test-Path -LiteralPath $partialPath -PathType Leaf) {
                Remove-Item -LiteralPath $partialPath -Force
            }
        }
    }
}

$scriptRootIsRepository = Test-BrainslugRepositoryRoot -Path $PSScriptRoot
$root = ""
if ($InstallDirectory -ne "") {
    $root = [IO.Path]::GetFullPath($InstallDirectory)
}
elseif ($scriptRootIsRepository) {
    $root = [IO.Path]::GetFullPath($PSScriptRoot)
}
else {
    $localAppData = [Environment]::GetFolderPath([Environment+SpecialFolder]::LocalApplicationData)
    if ([string]::IsNullOrWhiteSpace($localAppData)) {
        throw "Windows did not provide a local application data directory. Pass -InstallDirectory explicitly."
    }
    $root = Join-Path $localAppData "Brainslug"
}

if (-not (Test-BrainslugRepositoryRoot -Path $root)) {
    if (-not $BootstrapChild) {
        Write-BrainslugBootstrapBanner
    }
    if ($Branch.Contains("..")) {
        throw "Branch names containing '..' are not accepted by the standalone installer."
    }
    Write-Host "Install directory: $root" -ForegroundColor DarkGray
    Write-Host "Source: https://github.com/$Repository/tree/$Branch" -ForegroundColor DarkGray
    if (-not $PSCmdlet.ShouldProcess($root, "Download staged source and install Brainslug")) {
        Write-Host "Dry run complete. No files, processes, or scheduled tasks were changed." -ForegroundColor Yellow
        return
    }
    if (Test-Path -LiteralPath $root) {
        throw "The install directory already exists but is not a complete Brainslug installation: $root"
    }

    $temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("brainslug-install-" + [Guid]::NewGuid().ToString("N"))
    Assert-BrainslugBootstrapTemporaryPath -Path $temporaryRoot
    New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
    try {
        $escapedBranch = (($Branch -split "/") | ForEach-Object { [Uri]::EscapeDataString($_) }) -join "/"
        $archiveURI = [uri]("https://api.github.com/repos/" + $Repository + "/zipball/" + $escapedBranch)
        $headers = @{
            Accept = "application/vnd.github+json"
            "User-Agent" = "Brainslug-Windows-Installer"
            "X-GitHub-Api-Version" = "2022-11-28"
        }
        if ($GitHubToken -eq "" -and -not [string]::IsNullOrWhiteSpace($env:BRAINSLUG_GITHUB_TOKEN)) {
            $GitHubToken = $env:BRAINSLUG_GITHUB_TOKEN
        }
        if ($GitHubToken -ne "") {
            $headers.Authorization = "Bearer $GitHubToken"
        }
        $sourceRoot = Get-BrainslugBootstrapSource `
            -TemporaryRoot $temporaryRoot `
            -ArchiveURI $archiveURI `
            -Headers $headers
        $installParent = Split-Path -Parent $root
        if (-not (Test-Path -LiteralPath $installParent -PathType Container)) {
            New-Item -ItemType Directory -Path $installParent -Force | Out-Null
        }
        Move-Item -LiteralPath $sourceRoot -Destination $root
        $downloadedModule = Join-Path $root "scripts\windows\Brainslug.Windows.psm1"
        Import-Module $downloadedModule -Force
        Save-BrainslugSourceManifest `
            -SourceRoot $root `
            -DestinationRoot $root `
            -Repository $Repository `
            -Branch $Branch | Out-Null
        Invoke-BrainslugInstalledInstaller -Root $root
        return
    }
    finally {
        if (Test-Path -LiteralPath $temporaryRoot -PathType Container) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
}

$modulePath = Join-Path $root "scripts\windows\Brainslug.Windows.psm1"
Import-Module $modulePath -Force

if (-not $BootstrapChild) {
    Write-BrainslugStartBanner -Action "WINDOWS INSTALL"
}
Assert-BrainslugWindows

$needsSourceManifest = -not (Test-Path -LiteralPath (Join-Path $root ".git") -PathType Container) -and
    -not (Test-Path -LiteralPath (Join-Path $root ".brainslug-source.json") -PathType Leaf)

$startupChoice = $Startup
if ($startupChoice -eq "Prompt") {
    if ($NonInteractive) {
        $startupChoice = "Disable"
    }
    elseif (Read-BrainslugYesNo -Prompt "Start Brainslug automatically when you sign in to Windows?" -DefaultYes $true) {
        $startupChoice = "Enable"
    }
    else {
        $startupChoice = "Disable"
    }
}

Write-Host "Repository: $root" -ForegroundColor DarkGray
Write-Host "Startup choice: $startupChoice" -ForegroundColor DarkGray
if (-not $PSCmdlet.ShouldProcess($root, "Build and install Brainslug; configure startup: $startupChoice")) {
    Write-Host "Dry run complete. No files, processes, or scheduled tasks were changed." -ForegroundColor Yellow
    return
}

if ($needsSourceManifest) {
    Save-BrainslugSourceManifest `
        -SourceRoot $root `
        -DestinationRoot $root `
        -Repository $Repository `
        -Branch $Branch | Out-Null
}

$goExecutable = Get-BrainslugGo `
    -Root $root `
    -InstallWhenMissing $InstallGo.IsPresent `
    -Interactive (-not $NonInteractive.IsPresent)
$environmentPath = Join-Path $root ".env"
$environmentCreated = $false
if (-not (Test-Path -LiteralPath $environmentPath -PathType Leaf)) {
    Copy-Item -LiteralPath (Join-Path $root ".env.example") -Destination $environmentPath
    $environmentCreated = $true
    Write-Host "Created .env from .env.example." -ForegroundColor Yellow
}

if ($environmentCreated -and -not $NonInteractive) {
    if (Read-BrainslugYesNo -Prompt "Open .env in Notepad now?" -DefaultYes $true) {
        Start-Process -FilePath notepad.exe -ArgumentList ('"' + $environmentPath + '"') -Wait
    }
}

$binDirectory = Join-Path $root "bin"
$candidatePath = Join-Path $binDirectory "brainslug.next.exe"
$executablePath = Join-Path $binDirectory "brainslug.exe"
Invoke-BrainslugBuild `
    -Root $root `
    -GoExecutable $goExecutable `
    -CandidatePath $candidatePath `
    -SkipTests $SkipTests.IsPresent

$taskExists = Stop-BrainslugStartupTask -TaskName $TaskName
$processWasRunning = Stop-BrainslugProcess -ExecutablePath $executablePath
$backupPath = Install-BrainslugCandidate -Root $root -CandidatePath $candidatePath -ExecutablePath $executablePath

if ($startupChoice -eq "Enable") {
    Register-BrainslugStartupTask -Root $root -TaskName $TaskName
}
else {
    Unregister-BrainslugStartupTask -TaskName $TaskName
}

$started = $false
if (-not $NoStart) {
    if ($startupChoice -eq "Enable") {
        Start-BrainslugStartupTask -TaskName $TaskName
    }
    else {
        Start-BrainslugDetached -Root $root | Out-Null
    }
    $started = $true
    Wait-BrainslugHealth -Root $root | Out-Null
}

Write-BrainslugSuccessBanner -Message "BRAINSLUG INSTALLED"
Write-Host "Executable: $executablePath"
Write-Host "Configuration: $environmentPath"
if ($backupPath -and (Test-Path -LiteralPath $backupPath -PathType Leaf)) {
    Write-Host "Previous binary: $backupPath"
}
if ($startupChoice -eq "Enable") {
    Write-Host "Windows startup: enabled through Scheduled Task '$TaskName'"
}
else {
    Write-Host "Windows startup: disabled"
}
if ($started) {
    Write-Host "Dashboard: http://127.0.0.1:8081/"
    Write-Host "Initial setup: open the dashboard and create the local owner account."
}
elseif ($taskExists -or $processWasRunning) {
    Write-Host "Brainslug was stopped for installation. Start it with .\scripts\windows\run-brainslug.ps1"
}
Write-Host "Future updates: .\update.ps1"
