# Sobe o stack do Radiocheck (Docker + frontend) numa única invocação.
# Uso: .\scripts\start.ps1
#
# - Containers ficam rodando em background quando o script termina.
# - Ctrl+C para apenas o frontend; pare os containers com `docker compose ... down`.

$ErrorActionPreference = "Stop"

# Resolve a raiz do repo a partir do path do script (funciona se chamado de qualquer cwd).
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ComposeFile = Join-Path $RepoRoot "infra\docker\docker-compose.yml"
$EnvExample = Join-Path $RepoRoot "infra\docker\.env.example"
$EnvFile = Join-Path $RepoRoot "infra\docker\.env"
$FrontendDir = Join-Path $RepoRoot "frontend"

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }

# 1. Docker disponível?
Write-Step "checando docker"
& docker version --format '{{.Server.Version}}' > $null 2>&1
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERRO: docker nao esta rodando ou nao esta no PATH. Abra o Docker Desktop." -ForegroundColor Red
    exit 1
}

# 2. .env existe?
if (-not (Test-Path $EnvFile)) {
    Write-Step "criando infra/docker/.env a partir de .env.example"
    Copy-Item $EnvExample $EnvFile
}

# 3. Sobe containers.
Write-Step "docker compose up -d --build"
& docker compose -f $ComposeFile --env-file $EnvFile up -d --build
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERRO: docker compose falhou." -ForegroundColor Red
    exit 1
}

# 4. Espera a API ficar saudavel (max ~60s).
Write-Step "aguardando API em http://localhost:8080/v1/internal/health"
$apiPort = (Select-String -Path $EnvFile -Pattern '^API_PORT=(\d+)').Matches.Groups[1].Value
if (-not $apiPort) { $apiPort = "8080" }
$healthUrl = "http://localhost:$apiPort/v1/internal/health"
$ready = $false
for ($i = 0; $i -lt 30; $i++) {
    try {
        $resp = Invoke-WebRequest -Uri $healthUrl -UseBasicParsing -TimeoutSec 2
        if ($resp.StatusCode -eq 200) { $ready = $true; break }
    } catch { Start-Sleep -Seconds 2 }
}
if ($ready) {
    Write-Host "    API ok" -ForegroundColor Green
} else {
    Write-Host "    AVISO: API nao respondeu em 60s. Continuando mesmo assim. Veja: docker compose logs api" -ForegroundColor Yellow
}

# 5. Frontend: instalar deps se faltarem.
if (-not (Test-Path (Join-Path $FrontendDir "node_modules"))) {
    Write-Step "instalando dependencias do frontend (npm install)"
    Push-Location $FrontendDir
    try { & npm install } finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) {
        Write-Host "ERRO: npm install falhou." -ForegroundColor Red
        exit 1
    }
}

# 6. Roda o dev server em foreground (Ctrl+C para parar).
Write-Step "iniciando frontend em http://localhost:3000 (Ctrl+C para parar)"
Write-Host ""
Write-Host "  containers continuam rodando depois do Ctrl+C." -ForegroundColor DarkGray
Write-Host "  para parar tudo: docker compose -f infra/docker/docker-compose.yml --env-file infra/docker/.env down" -ForegroundColor DarkGray
Write-Host ""
Push-Location $FrontendDir
try { & npm run dev } finally { Pop-Location }
