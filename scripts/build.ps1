$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$frontend = Join-Path $root 'frontend\frontend'
$web = Join-Path $root 'backend\web\dist'
Push-Location $frontend
try {
    npm ci
    if ($LASTEXITCODE -ne 0) { throw 'npm ci failed. Stop Vite/dev servers that hold files in node_modules and retry.' }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw 'Frontend build failed.' }
} finally { Pop-Location }
if (Test-Path -LiteralPath $web) { Remove-Item -LiteralPath $web -Recurse -Force }
New-Item -ItemType Directory -Path $web -Force | Out-Null
Copy-Item -Path (Join-Path $frontend 'dist\*') -Destination $web -Recurse -Force
Push-Location (Join-Path $root 'backend')
try {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'Go tests failed.' }
    go build -o (Join-Path $root 'panel.exe') ./
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
} finally { Pop-Location }
Write-Host "Built $root\panel.exe"
