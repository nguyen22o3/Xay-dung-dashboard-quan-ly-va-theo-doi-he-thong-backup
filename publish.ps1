[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+$')]
    [string]$Version,
    [switch]$BuildOnly
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$repository = 'nguyen22o3/backup-monitor'
$targets = @(
    @{ Name = 'backend'; Context = 'backup-monitor-backend'; Stage = 'runtime' },
    @{ Name = 'host-tools'; Context = 'backup-monitor-backend'; Stage = 'host-tools' },
    @{ Name = 'frontend'; Context = 'backup-monitor-frontend'; Stage = $null }
)

function Invoke-ReleaseDocker {
    param([string[]]$Arguments)
    & docker @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "Docker command failed: docker $($Arguments -join ' ')"
    }
}

Get-Command docker -ErrorAction Stop | Out-Null
Invoke-ReleaseDocker -Arguments @('info', '--format', '{{.OSType}}')

# Never overwrite a published version. Authentication/network failures must not
# be mistaken for an absent tag. BuildOnly does not publish or query Hub tags.
if (-not $BuildOnly) {
    foreach ($target in $targets) {
        $reference = "${repository}:$($target.Name)-$Version"
        $probe = (& docker manifest inspect $reference 2>&1) -join [Environment]::NewLine
        $probeExit = $LASTEXITCODE
        if ($probeExit -eq 0) { throw "Published tag already exists: $reference. Choose a new version." }
        if ($probe -notmatch '(?i)no such manifest|manifest unknown') {
            throw "Cannot verify whether $reference already exists. No images were built/pushed. $probe"
        }
    }
}

# Build all three before publishing any of them. Dockerfiles only COPY approved
# application sources; runtime credentials/configuration are not release input.
foreach ($target in $targets) {
    $reference = "${repository}:$($target.Name)-$Version"
    $buildArguments = @('build', '--platform', 'linux/amd64')
    if ($target.Stage) { $buildArguments += @('--target', $target.Stage) }
    $buildArguments += @('-t', $reference, (Join-Path $PSScriptRoot $target.Context))
    Invoke-ReleaseDocker -Arguments $buildArguments
}

if ($BuildOnly) {
    Write-Host "Built all three images for $Version. Nothing was pushed."
    return
}

foreach ($target in $targets) {
    Invoke-ReleaseDocker -Arguments @('push', "${repository}:$($target.Name)-$Version")
}
Write-Host "Published all three images to $repository for $Version."
Write-Host 'Existing repositories and server deployments were not changed.'
