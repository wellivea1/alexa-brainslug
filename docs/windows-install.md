# Windows Install And Updates

Brainslug includes native PowerShell setup for a bare machine or source
checkout:

```text
install.ps1
update.ps1
scripts/windows/run-brainslug.ps1
```

The scripts do not require administrator rights, Git, `winget`, or a
preinstalled Go toolchain. They do not download a service wrapper, store a
Windows password, publish the admin port, or move private data out of the
installation.

## Startup Decision

The recommended Windows startup mechanism is a per-user Scheduled Task with an
**At log on** trigger.

This is a better fit than the common alternatives:

| Option | Decision | Reason |
| --- | --- | --- |
| Scheduled Task at logon | Used | Per-user, no stored password, hidden launch, working directory, restart policy |
| Registry `Run` key | Not used | Ordering and promptness are not guaranteed; no restart policy |
| Startup folder | Not used | Simple, but no process supervision or explicit working directory |
| Windows service | Not used | Requires elevation and a service wrapper; runs outside the owner's interactive profile |
| Scheduled Task at system startup | Not used | A passwordless interactive user token is not available before logon |

Brainslug is a single-owner process. Its dashboard is on user loopback and its
configuration, provider credentials, and local history belong to that owner.
Starting after that user signs in preserves the correct security and file
context without turning the application into a privileged system service.

The task is named `Brainslug` by default. It:

- runs only as the current user with limited privileges;
- starts when that specific user logs on;
- launches Windows PowerShell with a hidden window;
- uses the repository as its working directory;
- ignores duplicate launches;
- has no execution time limit;
- retries a failed process up to three times at one-minute intervals.

The hidden PowerShell process runs `scripts/windows/run-brainslug.ps1` and waits
for `bin/brainslug.exe`. Task Scheduler therefore tracks the complete server
lifetime instead of considering startup complete as soon as a detached child is
created.

## Install On A Bare Machine

This repository is private. On the bare machine:

1. Sign in to GitHub in a browser.
2. Open the repository and choose **Code -> Download ZIP**.
3. Extract the ZIP.
4. Inspect and run `install.ps1` from the extracted directory:

```powershell
Set-Location "path\to\alexa-brainslug-main"
notepad .\install.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

This path needs no Git client. It builds and runs from the extracted directory.

The same installer can bootstrap the rest of the repository when only
`install.ps1` has been copied to the machine. Because the repository is
private, put a fine-grained GitHub token with read-only repository contents
access in a process-scoped environment variable first:

```powershell
$env:BRAINSLUG_GITHUB_TOKEN = "temporary-read-only-token"
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
Remove-Item Env:BRAINSLUG_GITHUB_TOKEN
```

Avoid putting a long-lived or write-capable token in shell history. The token
is used only as an HTTP authorization header and is not written to the source
manifest, `.env`, task, or logs.

The standalone destination is `%LOCALAPPDATA%\Brainslug`. Choose another with
`-InstallDirectory`:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\install.ps1 `
  -InstallDirectory "D:\Apps\Brainslug"
```

The standalone installer downloads a GitHub source archive into a random
temporary directory. It retries the complete download, extraction, and source
structure check up to five times with backoff. Only a fully extracted source
tree is moved into the install directory. A `.partial` transfer is deleted and
never used as source.

No published checksum exists for a moving Git branch archive, so the source
archive is fetched through the authenticated GitHub API over HTTPS and
validated for the expected Brainslug Go module before installation. The
installer records SHA-256 hashes for every managed source file in
`.brainslug-source.json`; later updates refuse to overwrite local source edits.

## Install From A Checkout

Clone the repository, open PowerShell in its root, and run:

```powershell
.\install.ps1
```

The opening banner appears before setup begins. The installer then:

1. Confirms that it is running on Windows.
2. Locates Go 1.26 or newer.
3. Offers to download the current stable Go archive if Go is missing. The
   archive is retried, checked against the SHA-256 published by go.dev,
   extracted into staging, and installed under `.tools\go` only after it is
   complete.
4. Copies `.env.example` to `.env` only when `.env` does not exist.
5. Offers to open a new `.env` in Notepad.
6. Downloads modules, runs tests and vet, and builds a candidate binary.
7. Stops only a running process whose executable path exactly matches this
   checkout.
8. Keeps the previous binary as `bin/brainslug.previous.exe`.
9. Replaces `bin/brainslug.exe` with the tested candidate.
10. Applies the selected startup choice and starts Brainslug unless asked not
    to.
11. Checks the private health endpoint and prints a congratulatory banner.

The interactive startup question is:

```text
Start Brainslug automatically when you sign in to Windows? [Y/n]
```

Choose explicitly when scripting:

```powershell
.\install.ps1 -Startup Enable
.\install.ps1 -Startup Disable
```

Useful options:

| Option | Effect |
| --- | --- |
| `-InstallGo` | Install a verified user-local Go toolchain without asking when it is missing |
| `-InstallDirectory PATH` | Select the standalone installation directory |
| `-SkipTests` | Build without running tests or vet |
| `-NoStart` | Install the binary but do not start it now |
| `-NonInteractive` | Do not open Notepad or ask questions |
| `-Startup Enable` | Register the logon task |
| `-Startup Disable` | Remove the logon task if present |
| `-WhatIf` | Print the install decision without changing anything |

In non-interactive mode, an omitted startup choice defaults to disabled. This
keeps unattended execution opt-in rather than silently creating persistence.

## Update

Run updates from the installed checkout:

```powershell
.\update.ps1
```

For a Git checkout, the updater refuses to continue if the current branch is
wrong, tracked files have local changes, or the remote cannot be applied as a
fast-forward. For a standalone install, it first verifies every managed file
against `.brainslug-source.json`, then downloads and fully extracts a staged
archive before updating managed source. Git is not needed for this path.
Private standalone updates read `BRAINSLUG_GITHUB_TOKEN` from the current
process just like the initial bootstrap.

Network operations retry up to five times with exponential backoff. Downloads
use `.partial` files and are promoted only when complete. The Go archive also
must match its official SHA-256. Module downloads are retried by the Go command,
and the running binary and Scheduled Task are not stopped until the new source
passes tests, vet, and the candidate build. The updater then preserves the
prior binary, swaps in the candidate, and restarts Brainslug only when it was
already running or startup is enabled.

If a previously healthy foreground process does not become healthy after the
swap, the updater restores `brainslug.previous.exe`, restarts it, and reports a
failed update. Private data files are never part of the replacement.

Useful update options:

```powershell
.\update.ps1 -SkipTests
.\update.ps1 -NoRestart
.\update.ps1 -NonInteractive
.\update.ps1 -WhatIf
```

## Logs And Control

Hidden launches write fresh process logs to:

```text
logs/brainslug.stdout.log
logs/brainslug.stderr.log
```

The directory is ignored by Git. Provider keys and OAuth tokens are redacted by
the application, but prompts and local operational details may still be
private.

Inspect or control the startup task with built-in PowerShell commands:

```powershell
Get-ScheduledTask -TaskName Brainslug
Start-ScheduledTask -TaskName Brainslug
Stop-ScheduledTask -TaskName Brainslug
```

To remove automatic startup while keeping the installation:

```powershell
.\install.ps1 -Startup Disable -NoStart -SkipTests
```

## Security Notes

- Keep the checkout and its `.env`, database, settings, and auth files private.
- Do not change the Scheduled Task to run with highest privileges.
- Do not use a machine-wide startup task for this single-owner deployment.
- Do not expose port `8081`; startup automation does not add authentication.
- Review source changes before running `update.ps1` when the repository is not
  controlled solely by you.
- A branch archive is mutable. Pin and review a Git checkout when reproducible
  source provenance is required.
