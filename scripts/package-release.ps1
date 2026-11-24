param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9.-]+)?$')]
    [string]$Version,
    [ValidateSet('amd64','arm64')]
    [string]$Architecture = 'amd64'
)

$ErrorActionPreference = 'Stop'
$project = Split-Path -Parent $PSScriptRoot
$output = Join-Path $project 'bin'
New-Item -ItemType Directory -Force -Path $output | Out-Null
$name = "ownpanel-$Version-linux-$Architecture"
$archive = Join-Path $output "$name.tar.gz"
if (Test-Path -LiteralPath $archive) { throw "Release archive already exists: $archive" }
$stage = Join-Path $output ('.package-' + [Guid]::NewGuid().ToString('N'))
$bundle = Join-Path $stage $name
$originalOS = $env:GOOS
$originalArch = $env:GOARCH
$originalCGO = $env:CGO_ENABLED
Push-Location $project
try {
    New-Item -ItemType Directory -Path (Join-Path $bundle 'scripts') -Force | Out-Null
    $env:GOOS = 'linux'
    $env:GOARCH = $Architecture
    $env:CGO_ENABLED = '0'
    & go build -trimpath -o (Join-Path $bundle 'ownpanel-server') ./cmd/server
    if ($LASTEXITCODE) { throw 'Center build failed' }
    & go build -trimpath -o (Join-Path $bundle 'ownpanel-agent') ./cmd/agent
    if ($LASTEXITCODE) { throw 'Agent build failed' }
    $agentPackages = Join-Path $bundle "agents/linux-$Architecture"
    New-Item -ItemType Directory -Force -Path $agentPackages | Out-Null
    Copy-Item -LiteralPath (Join-Path $bundle 'ownpanel-agent') -Destination (Join-Path $agentPackages 'ownpanel-agent')
    & npm ci --prefix web --no-audit --no-fund
    if ($LASTEXITCODE) { throw 'Frontend dependency installation failed' }
    & npm run build --prefix web
    if ($LASTEXITCODE) { throw 'Frontend build failed' }
    Copy-Item -LiteralPath 'web/dist' -Destination (Join-Path $bundle 'web') -Recurse
    Copy-Item -LiteralPath 'README.md' -Destination $bundle
    Copy-Item -LiteralPath 'LICENSE' -Destination $bundle
    foreach ($script in @('install-center.sh','install-agent.sh','build-agent-nginx.sh','center-backup.py','upgrade-center.sh')) {
        $source = Join-Path $PSScriptRoot $script
        $destination = Join-Path $bundle "scripts/$script"
        [IO.File]::WriteAllText($destination, [IO.File]::ReadAllText($source).Replace("`r`n", "`n"))
    }
    $temporaryArchive = Join-Path $stage "$name.tar.gz"
    $linuxStage = (& wsl.exe -d Ubuntu -- wslpath -a ($stage.Replace('\','/'))).Trim()
    if ($LASTEXITCODE -or -not $linuxStage) { throw 'Cannot resolve package staging path in WSL' }
    & wsl.exe -d Ubuntu -- python3 scripts/package-linux.py "$linuxStage/$name" "$linuxStage/$name.tar.gz"
    if ($LASTEXITCODE) { throw 'Archive creation failed' }
    $hash = (Get-FileHash -LiteralPath $temporaryArchive -Algorithm SHA256).Hash.ToLowerInvariant()
    Move-Item -LiteralPath $temporaryArchive -Destination $archive
    [IO.File]::WriteAllText("$archive.sha256", "$hash  $name.tar.gz`n")
    Write-Output $archive
} finally {
    $env:GOOS = $originalOS
    $env:GOARCH = $originalArch
    $env:CGO_ENABLED = $originalCGO
    Pop-Location
    if (Test-Path -LiteralPath $stage) {
        $resolvedStage = (Resolve-Path -LiteralPath $stage).Path
        $resolvedOutput = (Resolve-Path -LiteralPath $output).Path
        if ((Split-Path -Parent $resolvedStage) -eq $resolvedOutput -and (Split-Path -Leaf $resolvedStage) -match '^\.package-[a-f0-9]{32}$') {
            Remove-Item -LiteralPath $resolvedStage -Recurse -Force
        }
    }
}
