Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$script:BrainslugTaskName = "Brainslug"
$script:MinimumGoMajor = 1
$script:MinimumGoMinor = 26
$script:DownloadAttempts = 5

function Write-BrainslugStartBanner {
    param([string]$Action = "SETUP")

    Write-Host @'
  ____            _           _
 | __ ) _ __ __ _(_)_ __  ___| |_   _  __ _
 |  _ \| '__/ _` | | '_ \/ __| | | | |/ _` |
 | |_) | | | (_| | | | | \__ \ | |_| | (_| |
 |____/|_|  \__,_|_|_| |_|___/_|\__,_|\__, |
                                        |___/
'@ -ForegroundColor Cyan
    Write-Host ("                     " + $Action) -ForegroundColor DarkCyan
    Write-Host ""
}

function Write-BrainslugSuccessBanner {
    param([string]$Message = "BRAINSLUG INSTALLED")

    Write-Host ""
    Write-Host @'
             _@)_
         ___/    \____
        /   ^    ^    `-.
        \_____\_/________/
             /| |\
'@ -ForegroundColor Green
    Write-Host ("        *** " + $Message + "! ***") -ForegroundColor Green
    Write-Host "   Small skill. Explicit brain. Private memory." -ForegroundColor DarkGreen
    Write-Host ""
}

function Assert-BrainslugWindows {
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) {
        throw "Brainslug's PowerShell setup scripts support Windows only."
    }
}

function Assert-BrainslugPathInsideRoot {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $resolvedRoot = [IO.Path]::GetFullPath($Root).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    $prefix = $resolvedRoot + [IO.Path]::DirectorySeparatorChar
    if (-not $resolvedPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify a path outside the Brainslug repository: $resolvedPath"
    }
}

function Assert-BrainslugTemporaryPath {
    param([Parameter(Mandatory = $true)][string]$Path)

    $temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd(
        [IO.Path]::DirectorySeparatorChar,
        [IO.Path]::AltDirectorySeparatorChar
    )
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    $prefix = $temporaryRoot + [IO.Path]::DirectorySeparatorChar + "brainslug-"
    if (-not $resolvedPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Refusing to modify an unexpected temporary path: $resolvedPath"
    }
}

function Invoke-BrainslugNative {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [Parameter(Mandatory = $true)][string[]]$Arguments,
        [Parameter(Mandatory = $true)][string]$Description
    )

    & $FilePath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE."
    }
}

function Invoke-BrainslugRetry {
    param(
        [Parameter(Mandatory = $true)][scriptblock]$Operation,
        [Parameter(Mandatory = $true)][string]$Description,
        [int]$Attempts = $script:DownloadAttempts,
        [double]$InitialDelaySeconds = 2
    )

    if ($Attempts -lt 1) {
        throw "Retry attempts must be positive."
    }
    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        try {
            return & $Operation
        }
        catch {
            if ($attempt -eq $Attempts) {
                throw "$Description failed after $Attempts attempts: $($_.Exception.Message)"
            }
            $delaySeconds = [Math]::Min($InitialDelaySeconds * [Math]::Pow(2, $attempt - 1), 15)
            Write-Warning "$Description was interrupted (attempt $attempt of $Attempts). Retrying in $delaySeconds seconds."
            if ($delaySeconds -gt 0) {
                Start-Sleep -Milliseconds ([int]($delaySeconds * 1000))
            }
        }
    }
}

function Invoke-BrainslugDownload {
    param(
        [Parameter(Mandatory = $true)][uri]$URI,
        [Parameter(Mandatory = $true)][string]$Destination,
        [string]$ExpectedSHA256 = "",
        [int]$Attempts = $script:DownloadAttempts,
        [hashtable]$Headers = @{}
    )

    [Net.ServicePointManager]::SecurityProtocol =
        [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    $expectedHash = $ExpectedSHA256
    $requestHeaders = $Headers
    $destinationDirectory = Split-Path -Parent $Destination
    if (-not (Test-Path -LiteralPath $destinationDirectory -PathType Container)) {
        New-Item -ItemType Directory -Path $destinationDirectory | Out-Null
    }
    $partialPath = $Destination + ".partial"
    try {
        Invoke-BrainslugRetry -Description "Download $URI" -Attempts $Attempts -Operation {
            if (Test-Path -LiteralPath $partialPath -PathType Leaf) {
                Remove-Item -LiteralPath $partialPath -Force
            }
            Invoke-WebRequest -Uri $URI -Headers $requestHeaders -OutFile $partialPath -UseBasicParsing
            if (-not (Test-Path -LiteralPath $partialPath -PathType Leaf)) {
                throw "The download did not create a file."
            }
            if ($expectedHash -ne "") {
                $actualHash = (Get-FileHash -LiteralPath $partialPath -Algorithm SHA256).Hash
                if (-not [string]::Equals($actualHash, $expectedHash, [StringComparison]::OrdinalIgnoreCase)) {
                    throw "SHA-256 mismatch. Expected $expectedHash, received $actualHash."
                }
            }
            Move-Item -LiteralPath $partialPath -Destination $Destination -Force
        } | Out-Null
    }
    finally {
        if (Test-Path -LiteralPath $partialPath -PathType Leaf) {
            Remove-Item -LiteralPath $partialPath -Force
        }
    }
}

function Get-BrainslugRelativePath {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$Path
    )

    $resolvedRoot = [IO.Path]::GetFullPath($Root).TrimEnd([IO.Path]::DirectorySeparatorChar, [IO.Path]::AltDirectorySeparatorChar)
    $resolvedPath = [IO.Path]::GetFullPath($Path)
    $prefix = $resolvedRoot + [IO.Path]::DirectorySeparatorChar
    if (-not $resolvedPath.StartsWith($prefix, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path is outside the source root: $resolvedPath"
    }
    return $resolvedPath.Substring($prefix.Length).Replace("\", "/")
}

function New-BrainslugSourceManifest {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string]$Branch
    )

    $files = @(
        Get-ChildItem -LiteralPath $SourceRoot -File -Recurse | Where-Object {
            $relativePath = Get-BrainslugRelativePath -Root $SourceRoot -Path $_.FullName
            $relativePath -ne ".brainslug-source.json" -and
            $relativePath -notmatch "^([.]git|[.]tools|[.]codex-verify|bin|data|dist|logs)/" -and
            $relativePath -ne ".env" -and
            $relativePath -notmatch "^brainslug[.]db" -and
            $relativePath -notmatch "[.](settings|auth)[.]json$"
        } | ForEach-Object {
            [PSCustomObject][ordered]@{
                Path = Get-BrainslugRelativePath -Root $SourceRoot -Path $_.FullName
                SHA256 = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
            }
        } | Sort-Object Path
    )
    return [PSCustomObject][ordered]@{
        Version = 1
        Method = "archive"
        Repository = $Repository
        Branch = $Branch
        Files = $files
    }
}

function Save-BrainslugSourceManifest {
    param(
        [Parameter(Mandatory = $true)][string]$SourceRoot,
        [Parameter(Mandatory = $true)][string]$DestinationRoot,
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string]$Branch
    )

    $manifest = New-BrainslugSourceManifest -SourceRoot $SourceRoot -Repository $Repository -Branch $Branch
    $manifestPath = Join-Path $DestinationRoot ".brainslug-source.json"
    $partialPath = $manifestPath + ".partial"
    Assert-BrainslugPathInsideRoot -Root $DestinationRoot -Path $manifestPath
    try {
        $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $partialPath -Encoding UTF8
        Move-Item -LiteralPath $partialPath -Destination $manifestPath -Force
    }
    finally {
        if (Test-Path -LiteralPath $partialPath -PathType Leaf) {
            Remove-Item -LiteralPath $partialPath -Force
        }
    }
    return $manifest
}

function Get-BrainslugSourceManifest {
    param([Parameter(Mandatory = $true)][string]$Root)

    $manifestPath = Join-Path $Root ".brainslug-source.json"
    if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "Archive source metadata is missing: $manifestPath"
    }
    $manifest = Get-Content -LiteralPath $manifestPath -Raw | ConvertFrom-Json
    if ($manifest.Version -ne 1 -or $manifest.Method -ne "archive" -or
        [string]::IsNullOrWhiteSpace($manifest.Repository) -or
        [string]::IsNullOrWhiteSpace($manifest.Branch)) {
        throw "Archive source metadata is invalid."
    }
    if ($manifest.Repository -notmatch "^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$" -or
        $manifest.Branch -notmatch "^[A-Za-z0-9._/-]+$" -or $manifest.Branch.Contains("..")) {
        throw "Archive source metadata contains an unsafe repository or branch."
    }
    return $manifest
}

function Test-BrainslugArchiveSourceClean {
    param([Parameter(Mandatory = $true)][string]$Root)

    $manifest = Get-BrainslugSourceManifest -Root $Root
    foreach ($entry in $manifest.Files) {
        $relativePath = [string]$entry.Path
        if ($relativePath -eq "" -or [IO.Path]::IsPathRooted($relativePath) -or $relativePath -match "(^|/)[.][.](/|$)") {
            throw "Archive source metadata contains an unsafe file path: $relativePath"
        }
        $path = Join-Path $Root $relativePath.Replace("/", "\")
        Assert-BrainslugPathInsideRoot -Root $Root -Path $path
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Managed source file is missing: $relativePath"
        }
        $actualHash = (Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash
        if (-not [string]::Equals($actualHash, [string]$entry.SHA256, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Managed source file has local changes: $relativePath"
        }
    }
    return $manifest
}

function Find-BrainslugSourceRoot {
    param([Parameter(Mandatory = $true)][string]$ExtractRoot)

    $moduleFiles = @(Get-ChildItem -LiteralPath $ExtractRoot -Filter go.mod -File -Recurse | Where-Object {
        (Get-Content -LiteralPath $_.FullName -TotalCount 1) -eq "module github.com/wellivea1/alexa-brainslug"
    })
    if ($moduleFiles.Count -ne 1) {
        throw "The source archive did not contain exactly one Brainslug repository."
    }
    return $moduleFiles[0].Directory.FullName
}

function Update-BrainslugArchiveSource {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$GitHubToken = ""
    )

    $oldManifest = Test-BrainslugArchiveSourceClean -Root $Root
    $temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("brainslug-source-" + [Guid]::NewGuid().ToString("N"))
    $archivePath = Join-Path $temporaryRoot "source.zip"
    $extractPath = Join-Path $temporaryRoot "extract"
    Assert-BrainslugTemporaryPath -Path $temporaryRoot
    New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
    try {
        $escapedBranch = (([string]$oldManifest.Branch -split "/") | ForEach-Object {
            [Uri]::EscapeDataString($_)
        }) -join "/"
        $archiveURI = [uri]("https://api.github.com/repos/" + $oldManifest.Repository + "/zipball/" + $escapedBranch)
        $headers = @{
            Accept = "application/vnd.github+json"
            "User-Agent" = "Brainslug-Windows-Updater"
            "X-GitHub-Api-Version" = "2022-11-28"
        }
        if ($GitHubToken -ne "") {
            $headers.Authorization = "Bearer $GitHubToken"
        }
        $sourceRoot = Invoke-BrainslugRetry -Description "Download and validate Brainslug source" -Operation {
            if (Test-Path -LiteralPath $archivePath -PathType Leaf) {
                Remove-Item -LiteralPath $archivePath -Force
            }
            if (Test-Path -LiteralPath $extractPath -PathType Container) {
                Remove-Item -LiteralPath $extractPath -Recurse -Force
            }
            Invoke-BrainslugDownload -URI $archiveURI -Destination $archivePath -Attempts 1 -Headers $headers
            Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
            Find-BrainslugSourceRoot -ExtractRoot $extractPath
        }
        $newManifest = New-BrainslugSourceManifest `
            -SourceRoot $sourceRoot `
            -Repository $oldManifest.Repository `
            -Branch $oldManifest.Branch
        $oldPaths = @{}
        foreach ($entry in $oldManifest.Files) {
            $oldPaths[[string]$entry.Path] = $true
        }
        $newPaths = @{}
        foreach ($entry in $newManifest.Files) {
            $newPaths[[string]$entry.Path] = $true
            $destination = Join-Path $Root ([string]$entry.Path).Replace("/", "\")
            Assert-BrainslugPathInsideRoot -Root $Root -Path $destination
            if ((Test-Path -LiteralPath $destination) -and -not $oldPaths.ContainsKey([string]$entry.Path)) {
                throw "The update would overwrite an unmanaged path: $($entry.Path)"
            }
        }

        foreach ($entry in $oldManifest.Files) {
            if (-not $newPaths.ContainsKey([string]$entry.Path)) {
                $obsoletePath = Join-Path $Root ([string]$entry.Path).Replace("/", "\")
                Assert-BrainslugPathInsideRoot -Root $Root -Path $obsoletePath
                if (Test-Path -LiteralPath $obsoletePath -PathType Leaf) {
                    Remove-Item -LiteralPath $obsoletePath -Force
                }
            }
        }
        foreach ($entry in $newManifest.Files) {
            $source = Join-Path $sourceRoot ([string]$entry.Path).Replace("/", "\")
            $destination = Join-Path $Root ([string]$entry.Path).Replace("/", "\")
            $destinationDirectory = Split-Path -Parent $destination
            if (-not (Test-Path -LiteralPath $destinationDirectory -PathType Container)) {
                New-Item -ItemType Directory -Path $destinationDirectory | Out-Null
            }
            Copy-Item -LiteralPath $source -Destination $destination -Force
        }
        Save-BrainslugSourceManifest `
            -SourceRoot $sourceRoot `
            -DestinationRoot $Root `
            -Repository $oldManifest.Repository `
            -Branch $oldManifest.Branch | Out-Null
        return $oldManifest
    }
    finally {
        if (Test-Path -LiteralPath $temporaryRoot -PathType Container) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
}

function Read-BrainslugYesNo {
    param(
        [Parameter(Mandatory = $true)][string]$Prompt,
        [bool]$DefaultYes = $true
    )

    $suffix = if ($DefaultYes) { "[Y/n]" } else { "[y/N]" }
    while ($true) {
        $answer = (Read-Host "$Prompt $suffix").Trim().ToLowerInvariant()
        if ($answer -eq "") {
            return $DefaultYes
        }
        if ($answer -eq "y" -or $answer -eq "yes") {
            return $true
        }
        if ($answer -eq "n" -or $answer -eq "no") {
            return $false
        }
        Write-Host "Please answer yes or no." -ForegroundColor Yellow
    }
}

function Get-BrainslugGo {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [bool]$InstallWhenMissing,
        [bool]$Interactive
    )

    $candidates = @()
    $localGo = Join-Path $Root ".tools\go\bin\go.exe"
    if (Test-Path -LiteralPath $localGo -PathType Leaf) {
        $candidates += $localGo
    }
    $goCommand = Get-Command go.exe -ErrorAction SilentlyContinue
    if ($null -ne $goCommand -and $candidates -notcontains $goCommand.Source) {
        $candidates += $goCommand.Source
    }

    $lastVersion = "not installed"
    foreach ($candidate in $candidates) {
        $versionOutput = (& $candidate version 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $versionOutput -match "go(?<major>[0-9]+)\.(?<minor>[0-9]+)") {
            $major = [int]$Matches["major"]
            $minor = [int]$Matches["minor"]
            $lastVersion = $versionOutput
            if ($major -gt $script:MinimumGoMajor -or
                ($major -eq $script:MinimumGoMajor -and $minor -ge $script:MinimumGoMinor)) {
                Write-Host "Using $versionOutput" -ForegroundColor DarkGray
                return $candidate
            }
        }
    }

    $shouldInstall = $InstallWhenMissing
    if (-not $shouldInstall -and $Interactive) {
        $shouldInstall = Read-BrainslugYesNo `
            -Prompt "Go 1.26 or newer was not found ($lastVersion). Install a verified user-local copy?" `
            -DefaultYes $true
    }
    if (-not $shouldInstall) {
        throw "Go 1.26 or newer is required; found $lastVersion. Rerun with -InstallGo to install a verified user-local copy."
    }

    $goExecutable = Install-BrainslugGo -Root $Root
    $versionOutput = (& $goExecutable version 2>&1 | Out-String).Trim()
    if ($LASTEXITCODE -ne 0 -or $versionOutput -notmatch "go(?<major>[0-9]+)\.(?<minor>[0-9]+)") {
        throw "Could not determine the installed Go version."
    }
    $major = [int]$Matches["major"]
    $minor = [int]$Matches["minor"]
    if ($major -lt $script:MinimumGoMajor -or
        ($major -eq $script:MinimumGoMajor -and $minor -lt $script:MinimumGoMinor)) {
        throw "The downloaded toolchain is too old: $versionOutput."
    }
    Write-Host "Using $versionOutput" -ForegroundColor DarkGray
    return $goExecutable
}

function Install-BrainslugGo {
    param([Parameter(Mandatory = $true)][string]$Root)

    Assert-BrainslugPathInsideRoot -Root $Root -Path (Join-Path $Root ".tools\go")
    $isArm64 = $env:PROCESSOR_ARCHITECTURE -eq "ARM64" -or $env:PROCESSOR_ARCHITEW6432 -eq "ARM64"
    $architecture = if ($isArm64) { "arm64" } else { "amd64" }
    Write-Host "Finding the current stable Go toolchain..." -ForegroundColor Cyan
    $releases = Invoke-BrainslugRetry -Description "Go release lookup" -Operation {
        Invoke-RestMethod -Uri "https://go.dev/dl/?mode=json" -Method Get -TimeoutSec 30
    }
    $release = $releases | Where-Object { $_.stable } | Select-Object -First 1
    if ($null -eq $release) {
        throw "The Go download service returned no stable release."
    }
    $file = $release.files | Where-Object {
        $_.os -eq "windows" -and $_.arch -eq $architecture -and $_.kind -eq "archive"
    } | Select-Object -First 1
    if ($null -eq $file -or [string]::IsNullOrWhiteSpace($file.sha256)) {
        throw "The Go download service returned no verified Windows $architecture archive."
    }

    $temporaryRoot = Join-Path ([IO.Path]::GetTempPath()) ("brainslug-go-" + [Guid]::NewGuid().ToString("N"))
    $archivePath = Join-Path $temporaryRoot $file.filename
    $extractPath = Join-Path $temporaryRoot "extract"
    Assert-BrainslugTemporaryPath -Path $temporaryRoot
    New-Item -ItemType Directory -Path $temporaryRoot | Out-Null
    try {
        Write-Host "Downloading $($file.filename) with retry and SHA-256 verification..." -ForegroundColor Cyan
        Invoke-BrainslugDownload `
            -URI ("https://go.dev/dl/" + $file.filename) `
            -Destination $archivePath `
            -ExpectedSHA256 $file.sha256
        Expand-Archive -LiteralPath $archivePath -DestinationPath $extractPath -Force
        $extractedGo = Join-Path $extractPath "go"
        if (-not (Test-Path -LiteralPath (Join-Path $extractedGo "bin\go.exe") -PathType Leaf)) {
            throw "The verified Go archive did not contain go.exe."
        }
        $toolsRoot = Join-Path $Root ".tools"
        $installedRoot = Join-Path $toolsRoot "go"
        $candidateRoot = Join-Path $toolsRoot ("go.next." + [Guid]::NewGuid().ToString("N"))
        $backupRoot = Join-Path $toolsRoot "go.previous"
        Assert-BrainslugPathInsideRoot -Root $Root -Path $candidateRoot
        Assert-BrainslugPathInsideRoot -Root $Root -Path $backupRoot
        if (-not (Test-Path -LiteralPath $toolsRoot -PathType Container)) {
            New-Item -ItemType Directory -Path $toolsRoot | Out-Null
        }
        Move-Item -LiteralPath $extractedGo -Destination $candidateRoot
        if (-not (Test-Path -LiteralPath (Join-Path $candidateRoot "bin\go.exe") -PathType Leaf)) {
            throw "The staged Go toolchain is incomplete."
        }
        if (Test-Path -LiteralPath $backupRoot -PathType Container) {
            Remove-Item -LiteralPath $backupRoot -Recurse -Force
        }
        if (Test-Path -LiteralPath $installedRoot -PathType Container) {
            Move-Item -LiteralPath $installedRoot -Destination $backupRoot
        }
        try {
            Move-Item -LiteralPath $candidateRoot -Destination $installedRoot
        }
        catch {
            if (Test-Path -LiteralPath $backupRoot -PathType Container) {
                Move-Item -LiteralPath $backupRoot -Destination $installedRoot
            }
            if (Test-Path -LiteralPath $candidateRoot -PathType Container) {
                Remove-Item -LiteralPath $candidateRoot -Recurse -Force
            }
            throw
        }
        if (Test-Path -LiteralPath $backupRoot -PathType Container) {
            Remove-Item -LiteralPath $backupRoot -Recurse -Force
        }
        return Join-Path $installedRoot "bin\go.exe"
    }
    finally {
        if (Test-Path -LiteralPath $temporaryRoot -PathType Container) {
            Remove-Item -LiteralPath $temporaryRoot -Recurse -Force
        }
    }
}

function Invoke-BrainslugBuild {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$GoExecutable,
        [Parameter(Mandatory = $true)][string]$CandidatePath,
        [bool]$SkipTests
    )

    Assert-BrainslugPathInsideRoot -Root $Root -Path $CandidatePath
    $binDirectory = Split-Path -Parent $CandidatePath
    if (-not (Test-Path -LiteralPath $binDirectory -PathType Container)) {
        New-Item -ItemType Directory -Path $binDirectory | Out-Null
    }
    if (Test-Path -LiteralPath $CandidatePath -PathType Leaf) {
        Remove-Item -LiteralPath $CandidatePath -Force
    }

    Push-Location $Root
    try {
        Write-Host "Downloading Go modules..." -ForegroundColor Cyan
        Invoke-BrainslugRetry -Description "Go module download" -Operation {
            Invoke-BrainslugNative -FilePath $GoExecutable -Arguments @("mod", "download") -Description "Go module download"
        } | Out-Null
        if (-not $SkipTests) {
            Write-Host "Running tests and vet..." -ForegroundColor Cyan
            Invoke-BrainslugNative -FilePath $GoExecutable -Arguments @("test", "./...") -Description "Go tests"
            Invoke-BrainslugNative -FilePath $GoExecutable -Arguments @("vet", "./...") -Description "Go vet"
        }
        Write-Host "Building Brainslug..." -ForegroundColor Cyan
        Invoke-BrainslugNative -FilePath $GoExecutable -Arguments @(
            "build", "-trimpath", "-ldflags=-s -w", "-o", $CandidatePath, "./cmd/brainslug"
        ) -Description "Brainslug build"
    }
    finally {
        Pop-Location
    }
}

function Get-BrainslugTask {
    param([string]$TaskName = $script:BrainslugTaskName)

    Import-Module ScheduledTasks -ErrorAction Stop
    return Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
}

function Get-BrainslugWindowsPowerShell {
    $systemRoot = [Environment]::GetFolderPath([System.Environment+SpecialFolder]::Windows)
    $windowsPowerShell = Join-Path $systemRoot "System32\WindowsPowerShell\v1.0\powershell.exe"
    if (-not (Test-Path -LiteralPath $windowsPowerShell -PathType Leaf)) {
        throw "Windows PowerShell was not found at $windowsPowerShell."
    }
    return $windowsPowerShell
}

function Register-BrainslugStartupTask {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [string]$TaskName = $script:BrainslugTaskName
    )

    Import-Module ScheduledTasks -ErrorAction Stop
    $runner = Join-Path $Root "scripts\windows\run-brainslug.ps1"
    if (-not (Test-Path -LiteralPath $runner -PathType Leaf)) {
        throw "Scheduled-task runner is missing: $runner"
    }
    $windowsPowerShell = Get-BrainslugWindowsPowerShell
    $arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $runner + '"'
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent().Name
    $action = New-ScheduledTaskAction -Execute $windowsPowerShell -Argument $arguments -WorkingDirectory $Root
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $identity
    $principal = New-ScheduledTaskPrincipal -UserId $identity -LogonType Interactive -RunLevel Limited
    $settings = New-ScheduledTaskSettingsSet `
        -AllowStartIfOnBatteries `
        -DontStopIfGoingOnBatteries `
        -StartWhenAvailable `
        -RestartCount 3 `
        -RestartInterval (New-TimeSpan -Minutes 1) `
        -ExecutionTimeLimit ([TimeSpan]::Zero) `
        -MultipleInstances IgnoreNew
    Register-ScheduledTask `
        -TaskName $TaskName `
        -Action $action `
        -Trigger $trigger `
        -Principal $principal `
        -Settings $settings `
        -Description "Run the local Brainslug Alexa gateway when this user signs in." `
        -Force | Out-Null
    Write-Host "Startup task '$TaskName' is enabled for $identity." -ForegroundColor Green
}

function Unregister-BrainslugStartupTask {
    param([string]$TaskName = $script:BrainslugTaskName)

    $task = Get-BrainslugTask -TaskName $TaskName
    if ($null -ne $task) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
        Write-Host "Startup task '$TaskName' was removed." -ForegroundColor Yellow
    }
}

function Stop-BrainslugStartupTask {
    param([string]$TaskName = $script:BrainslugTaskName)

    $task = Get-BrainslugTask -TaskName $TaskName
    if ($null -ne $task -and $task.State -eq "Running") {
        Stop-ScheduledTask -TaskName $TaskName
    }
    return $null -ne $task
}

function Stop-BrainslugProcess {
    param([Parameter(Mandatory = $true)][string]$ExecutablePath)

    $expected = [IO.Path]::GetFullPath($ExecutablePath)
    $stopped = $false
    $processes = Get-CimInstance Win32_Process -ErrorAction SilentlyContinue | Where-Object {
        $_.ExecutablePath -and [string]::Equals([IO.Path]::GetFullPath($_.ExecutablePath), $expected, [StringComparison]::OrdinalIgnoreCase)
    }
    foreach ($process in $processes) {
        Stop-Process -Id $process.ProcessId -ErrorAction Stop
        Wait-Process -Id $process.ProcessId -Timeout 15 -ErrorAction SilentlyContinue
        $stopped = $true
    }
    return $stopped
}

function Install-BrainslugCandidate {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [Parameter(Mandatory = $true)][string]$CandidatePath,
        [Parameter(Mandatory = $true)][string]$ExecutablePath
    )

    Assert-BrainslugPathInsideRoot -Root $Root -Path $CandidatePath
    Assert-BrainslugPathInsideRoot -Root $Root -Path $ExecutablePath
    if (-not (Test-Path -LiteralPath $CandidatePath -PathType Leaf)) {
        throw "Built candidate is missing: $CandidatePath"
    }
    $backupPath = Join-Path (Split-Path -Parent $ExecutablePath) "brainslug.previous.exe"
    Assert-BrainslugPathInsideRoot -Root $Root -Path $backupPath
    if (Test-Path -LiteralPath $backupPath -PathType Leaf) {
        Remove-Item -LiteralPath $backupPath -Force
    }
    if (Test-Path -LiteralPath $ExecutablePath -PathType Leaf) {
        Move-Item -LiteralPath $ExecutablePath -Destination $backupPath
    }
    try {
        Move-Item -LiteralPath $CandidatePath -Destination $ExecutablePath
    }
    catch {
        if (Test-Path -LiteralPath $backupPath -PathType Leaf) {
            Move-Item -LiteralPath $backupPath -Destination $ExecutablePath
        }
        throw
    }
    return $backupPath
}

function Start-BrainslugDetached {
    param([Parameter(Mandatory = $true)][string]$Root)

    $runner = Join-Path $Root "scripts\windows\run-brainslug.ps1"
    $windowsPowerShell = Get-BrainslugWindowsPowerShell
    $arguments = '-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -WindowStyle Hidden -File "' + $runner + '"'
    return Start-Process -FilePath $windowsPowerShell -ArgumentList $arguments -WorkingDirectory $Root -WindowStyle Hidden -PassThru
}

function Start-BrainslugStartupTask {
    param([string]$TaskName = $script:BrainslugTaskName)

    $task = Get-BrainslugTask -TaskName $TaskName
    if ($null -eq $task) {
        throw "Startup task '$TaskName' is not registered."
    }
    Start-ScheduledTask -TaskName $TaskName
}

function Get-BrainslugAdminHealthURI {
    param([Parameter(Mandatory = $true)][string]$Root)

    $address = "127.0.0.1:8081"
    $environmentFile = Join-Path $Root ".env"
    if (Test-Path -LiteralPath $environmentFile -PathType Leaf) {
        foreach ($line in Get-Content -LiteralPath $environmentFile) {
            if ($line -match "^\s*ADMIN_SERVER_ADDR\s*=\s*(?<value>.+?)\s*$") {
                $address = $Matches["value"].Trim().Trim('"').Trim("'")
            }
        }
    }
    if ($address -match "^:(?<port>[0-9]+)$") {
        return "http://127.0.0.1:$($Matches['port'])/health"
    }
    if ($address -match "^\[(?<host>[^]]+)\]:(?<port>[0-9]+)$") {
        $hostName = $Matches["host"]
        if ($hostName -eq "::" -or $hostName -eq "::0") {
            $hostName = "::1"
        }
        return "http://[$hostName]:$($Matches['port'])/health"
    }
    if ($address -match "^(?<host>[^:]+):(?<port>[0-9]+)$") {
        $hostName = $Matches["host"]
        if ($hostName -eq "0.0.0.0") {
            $hostName = "127.0.0.1"
        }
        return "http://$hostName`:$($Matches['port'])/health"
    }
    return "http://127.0.0.1:8081/health"
}

function Wait-BrainslugHealth {
    param(
        [Parameter(Mandatory = $true)][string]$Root,
        [int]$TimeoutSeconds = 12
    )

    $healthURI = Get-BrainslugAdminHealthURI -Root $Root
    $deadline = [DateTime]::UtcNow.AddSeconds($TimeoutSeconds)
    while ([DateTime]::UtcNow -lt $deadline) {
        try {
            $response = Invoke-RestMethod -Uri $healthURI -Method Get -TimeoutSec 2
            if ($response.status -eq "ok") {
                Write-Host "Brainslug is healthy at $healthURI" -ForegroundColor Green
                return $true
            }
        }
        catch {
            Write-Verbose "Brainslug health is not ready: $($_.Exception.Message)"
        }
        Start-Sleep -Milliseconds 400
    }
    Write-Warning "Brainslug did not become healthy at $healthURI. Check .env and logs\brainslug.stderr.log."
    return $false
}

Export-ModuleMember -Function @(
    "Write-BrainslugStartBanner",
    "Write-BrainslugSuccessBanner",
    "Assert-BrainslugWindows",
    "Assert-BrainslugPathInsideRoot",
    "Invoke-BrainslugNative",
    "Invoke-BrainslugRetry",
    "Invoke-BrainslugDownload",
    "Read-BrainslugYesNo",
    "Get-BrainslugGo",
    "Install-BrainslugGo",
    "Save-BrainslugSourceManifest",
    "Get-BrainslugSourceManifest",
    "Test-BrainslugArchiveSourceClean",
    "Update-BrainslugArchiveSource",
    "Invoke-BrainslugBuild",
    "Get-BrainslugTask",
    "Register-BrainslugStartupTask",
    "Unregister-BrainslugStartupTask",
    "Stop-BrainslugStartupTask",
    "Stop-BrainslugProcess",
    "Install-BrainslugCandidate",
    "Start-BrainslugDetached",
    "Start-BrainslugStartupTask",
    "Get-BrainslugAdminHealthURI",
    "Wait-BrainslugHealth"
)
