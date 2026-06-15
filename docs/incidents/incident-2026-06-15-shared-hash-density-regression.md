---
status: resolvido
severidade: CRÍTICA
ultima-verificacao: 2026-06-15
codigo-relacionado:
  - workers/internal/sharing/sharing.go
  - workers/internal/sharing/sharing_test.go
  - workers/internal/sharing/subscriber.go
  - workers/cmd/backfill-shared-hashes/main.go
  - workers/cmd/selfmatch/main.go
  - workers/pkg/audio/peaks.go
  - fingerprint/fingerprint/generator.py
  - infra/docker/docker-compose.yml
  - infra/docker/Dockerfiles/workers.Dockerfile
---

# INCIDENTE 2026-06-15 — Regressão de shared-hash pela densidade do #2 (65% do catálogo cego)

> **RESOLVIDO em 2026-06-15.** Ver seção 0 para o desfecho. A causa raiz (shared-hash
> denso) está confirmada E o fix está deployado e validado. O histórico de investigação
> (seções 1–10) fica preservado como referência.

---

## 0. Desfecho (RESOLVIDO — 2026-06-15)

**Causa raiz confirmada e única:** shared-hash com `MinScore=5` baixo demais para a
densidade do #2. Não havia segundo bug — investigado e descartado (ver "Investigação do
falso-segundo-bug" abaixo).

**Fix aplicado:**
- Commit `f1e375c`: `const MinScore=5` → `DefaultMinScore=20` + `MinScore()`/
  `minScoreFromEnv()` lendo `SHARING_MIN_SCORE`, plumbado por `MarkSharedHashes` →
  `scanForSharedRegions` (ponto único que governa o subscriber de upload **e** o
  `backfill-shared-hashes`). Passthrough `SHARING_MIN_SCORE` no service `api` do
  `docker-compose.yml` (sem ele a env não chegava no container). TDD: `TestMinScoreFromEnv`.
- Commit `500f4cc`: `cmd/selfmatch` — diagnóstico read-only reusável (ver abaixo).
- **Decisão do usuário:** fix direto (sem a mitigação de emergência 5.5).

**`MinScore` final = 20** (default; `SHARING_MIN_SCORE` deixado vazio no `.env`). Deploy
(`build api` → `--force-recreate --no-deps api`) + reset `is_shared=false` + re-backfill
feitos em prod.

**Validação 5.4 (pós-backfill, MinScore=20):**
```
shared_100=0  shared_90mais=0  shared_medio=0  shared_baixo=94  total=94
93/96/97/102 + 77: todos 0.0% shared
```
Quase nada flagar a 20 é o resultado **correto**, não overshoot: um sting/jingle genuíno é
áudio idêntico → pontua 50+ no scan (quase self-match) → continua sendo flagado a 20.
Subir 5→20 derruba só o ruído de densidade (6-12), não sting genuíno. (Nota: o agregado só
diz `<30%`; um jingle legítimo de 5s/30s ≈ 17% cairia em `shared_baixo` sem aparecer como
0 — então "0% em todo catálogo" é impreciso; o que se confirmou foi 0.0% nos amostrados +
ROGGA, e a ausência de sting forte não-flagado, ver abaixo.)

**Verificação de proteção contra falso-positivo (jingle do ROGGA, via `selfmatch` best-OTHER):**
o usuário cobrou o que acontece com o jingle do ROGGA agora que o shared zerou. Medido o
cross-match entre os spots ROGGA:
- Spots de produto distintos (URBAN BAVIERA 5, EVOLUTION 6, POLINESIA 7, AZALÉIA 39,
  HANNOVER 41, DESDREN 43): cross-match pico **9-12** = ruído. **Não compartilham jingle
  forte** → nada a proteger → 0% correto.
- `JINGLE VERÃO 30` (1) → 518 contra id 21 (**duplicata** exata) e ⊂ `VERÃO 60` (3) =
  **subset** → corretamente não-flagado (dedup/version-disambiguation tratam).
- PULSO SONORO (8/34/36/51) <10s → pulados por `MinShareableDuration` (inalterado).

Conclusão: **o fix NÃO abriu buraco de FP.** O `MinScore=5` antigo flagava o ruído 9-12
inter-ROGGA como falso "sting" (comendo hashes únicas → contribuindo pra cegueira); o 20
rejeita esse ruído e ainda pegaria jingle real (50+). Para uploads futuros, sting genuíno
(50+) segue flagado automaticamente — sistema calibrado, não inerte.

**Higiene de catálogo (lateral):** id 1 e id 21 são o mesmo material duplicado (hashes
idênticas) — operador deve remover um. Não afeta detecção.

### Investigação do falso-segundo-bug (o usuário desconfiou; testamos)

O usuário notou que materiais não detectavam e levantou "o problema não é só o shaded".
Investigado por **debugging sistemático** (sem chute), três hipóteses testadas e refutadas:

1. **Regressão global da migração?** ❌ Volume de detecção saudável atravessando o
   re-fingerprint (~47 materiais/dia, 700+ det/dia útil); **ASAAS detectou 16:44 do dia
   do fix**. Lockstep e pipeline OK globalmente.
2. **"Material único da STIHL" é um material não-compartilhado falhando?** ❌ É o próprio
   **short_id 102** (único material da campanha 236), que estava 100% shared → explicado
   pelo fix, não é causa nova.
3. **Fingerprint dos cegos quebrado (não casa com o ao vivo)?** ❌ **Refutado pelo
   `selfmatch`**: self-score 77=355 (controle), **102=585, 97=418** — fingerprints
   impecáveis, lockstep `peaks.go ↔ generator.py` perfeito. O ruído 6-8 no window scan é
   só janela-sem-veiculação; quando airarem pontuam centenas e detectam.

**`det_total=0` dos 4** = cegueira vitalícia do shared-hash (até o backfill) + ainda não
terem ido ao ar no intervalo desde o fix. **Checkpoint operacional restante:** ver a 1ª
veiculação pós-fix entrar (`det_total` subir). Não é código.

### `cmd/selfmatch` (ferramenta reusável)

`selfmatch --short-id N` decodifica o master e roda o `MatchWindow` Go real contra as
hashes armazenadas, reportando o **self-score** (**40+ = fingerprint ok / lockstep vale;
~6-8 = quebrado**) e o **best-OTHER score** (maior cross-match contra outro material —
serve pra checar sting/jingle compartilhado: alto = compartilha segmento forte). Read-only
(SELECT + decode). Útil pra toda futura migração de densidade (valida lockstep sem esperar
veiculação) e pra auditar proteção de FP — relacionado ao follow-up "índice versionado".

### Follow-ups abertos (não-bloqueantes)

- `internal/similarity/similarity.go:28` (`MinScore=5`, aviso de duplicata no upload) sofre
  da mesma densidade do #2, mas NÃO bloqueia detecção — avaliar depois.
- Recalibração de `station_thresholds` pós-#2 (já listado na seção 8 #2): o piso de ruído
  subiu pra 6-8; conferir que os thresholds por estação seguem confortavelmente acima.
- Confirmar `det_total` dos 4 subindo após a 1ª veiculação pós-fix.

---

## 1. TL;DR (leia isto primeiro)

**Sintoma:** campanhas ativas, com material `ready`, emissoras monitoradas, **não geram
nenhuma detecção** — enquanto o fornecedor externo capta normalmente.

**Causa raiz (CONFIRMADA):** a detecção de shared-hash (`workers/internal/sharing/sharing.go`)
marca uma janela como "compartilhada" quando **outro material pontua ≥ `MinScore` (=5)**
naquela janela. O fix **#2** (peak-picking ~4× mais denso, commit `3bd7178`, já em prod)
**subiu o piso de ruído** material-vs-material de `<5` para `~6-8`. Resultado: ao escanear
um material novo, **algum** material do catálogo pontua ≥5 em **quase toda janela** →
**~100% das hashes marcadas `is_shared=true`** → `UniqueScore` (que a confirmação usa) = **0**
→ **matematicamente impossível detectar**, por mais que o comercial toque.

**Blast radius:** **37 materiais a 100% shared, 61 a ≥90% shared, de 94 ready = ~65% do
catálogo cego.** Todo material fingerprintado **depois** que o catálogo virou denso (pós-#2,
~2026-06-08) nasce indetectável. E continua assim pra cada novo upload até o fix.

**O fix:** subir o `MinScore` do sharing pra acompanhar a densidade do #2 (de `5` pra
~`15-25`, validado), tornar configurável via env, **resetar `is_shared` + re-rodar o
backfill** com o threshold novo. TDD. Detalhes na seção 5.

**Quem causou:** o #2 (a densidade). Foi implementado nesta sessão pra resolver recall de
áudio curto — funciona pra isso (ASAAS detecta), mas furou o `MinScore=5` do sharing, que
ficou pra trás.

---

## 2. Contexto: o que foi feito nesta sessão (3 fixes JÁ em prod)

Tudo já commitado e pushado na `master` (o usuário pediu pra commitar direto na main):

| Commit | O quê | Status |
|--------|-------|--------|
| `3bd7178` | **#2 densidade**: peak-picking 4× mais denso (`peaks.go` neighborFrames 8→3, neighborBins 8→6; `generator.py` PEAK_NEIGHBORHOOD_T 17→7, F 17→13). **LOCKSTEP Go↔Python.** Recall de áudio curto. | Em prod. **É a causa deste incidente.** |
| `eda694b` | docs: máquina migrada pra `c3-highcpu-8` (8 vCPU, 16 GB). | Em prod |
| `d368afa` | **-vn no broadcast_sim**: fingerprint falhava (exit 234) em master com stream de vídeo h264. Add `-vn`. | Em prod |
| `20f9e13` | **retract de material (§18.2.2)**: o `UPDATE retracted_at` fazia JOIN só em `commercials`; pra material puro batia 0 linhas → corte de 15s não era retraído → dupla contagem. Fix: resolver short_id via `commercials UNION materials` (`markDetectionRetracted`). | Em prod |

**Migração do #2:** o usuário rodou `./scripts/deploy.sh` + re-fingerprintou toda a base
(script que publica `fingerprint.generate` por material). Foi ESSE re-fingerprint denso que
deixou o catálogo todo denso e disparou a regressão de shared-hash. Doc da migração:
[docs/operations/refingerprint-density-migration.md](../operations/refingerprint-density-migration.md).

**Importante (lockstep):** `peaks.go` (Go, query ao vivo) e `generator.py` (Python, masters)
TÊM que ter o peak-picking casado. NÃO mexa em um sem o outro, e mudar qualquer um exige
re-fingerprint atômico de toda a base. Valores atuais: Go `neighborFrames=3, neighborBins=6`
↔ Python `PEAK_NEIGHBORHOOD_T=7, PEAK_NEIGHBORHOOD_F=13`.

---

## 3. Como o shared-hash funciona (pra entender o fix)

Doc canônico: [docs/architecture/shared-hash-detection.md](../architecture/shared-hash-detection.md).

**Propósito:** evitar falso-positivo quando dois comerciais compartilham um trecho (sting,
vinheta, jingle). Sem ele, quando o comercial A toca, o trecho compartilhado faz o matcher
acumular cobertura no comercial B e confirmar B falsamente.

**Algoritmo** (`MarkSharedHashes` em `sharing.go`):
1. Para cada material novo Y, carrega o índice de TODOS os comerciais ready.
2. Decodifica o master de Y (pipeline `loudnorm + highpass + lowpass`).
3. Desliza janela de **4s @ 1s hop** sobre o PCM de Y. Pra cada janela onde **outro**
   comercial X pontua **≥ `MinScore` (=5)** → marca o range de frames de Y (e de X).
4. Classifica cada par (Y, X): `fraction = janelas_com_hit / total_janelas`.
   - `fraction ≥ SubsetThreshold (0.5)` → subset/duplicata → **NÃO flaga** (corte de
     30s ⊂ 60s; a desambiguação por duração resolve).
   - `fraction < 0.5` → sting → **flaga os dois lados**.
5. `UPDATE fingerprint_hashes SET is_shared = true` nos ranges merged.

**No runtime:** o matcher conta TODOS os hits (robustez), mas o state machine só credita
hits de hashes **não-compartilhados** (`UniqueScore`) pra confirmar. **Material com 100%
shared → UniqueScore sempre 0 → nunca sai de `StateIdle` → nunca detecta.**

**Constantes** (`sharing.go:35-57`):
```go
WindowSeconds   = 4
HopSeconds      = 1
MinScore        = 5    // ← O BUG. "aligned with the runtime matcher's default minScore"
SubsetThreshold = 0.5
MinShareableDurationSeconds = 10.0  // pula comerciais < 10s
```

**Por que o #2 quebrou:** `MinScore=5` foi calibrado pro fingerprint ESPARSO antigo.
O comentário em `sharing.go:64-70` JÁ avisava do risco de "fatias finas que cumulativamente
flagam quase tudo" (visto antes com material curto, RÔGGA PULSO ~67%). O #2 (4× mais hashes)
subiu o score de fundo material-vs-material pra ~6-8 → `≥5` em quase toda janela → **muitos
materiais X diferentes** flagam **fatias diferentes** de Y (cada um `< SubsetThreshold`, então
o subset-protection NÃO salva) → cumulativamente **100% de Y vira shared**.

**Gatilho do flag:** automático após cada upload (evento `fingerprint.shared-scan` →
`sharing.Subscriber` → `MarkSharedHashes`). Backfill manual:
`docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"`.

---

## 4. Trilha de evidência (o que JÁ foi descartado — NÃO re-investigue)

Bisecção completa feita nesta sessão. Tudo abaixo já foi verificado em prod:

| Hipótese | Veredito | Como se sabe |
|----------|----------|--------------|
| Scheduler não transiciona programada→ativa | ❌ descartado | Todas as campanhas estão `ativa` (query de status) |
| Material não está `ready` | ❌ descartado | `fingerprint_status='ready'` em todos |
| `campaigns.target_stations` vazio | ❌ descartado | `camp_stations > 0` em todas |
| Divergência `campaigns.target_stations` vs `campaign_materials.target_stations` | ❌ descartado | `com_material = camp_stations` em todas |
| Emissoras não monitoradas (worker não sobe) | ❌ descartado | **As 11 emissoras do NATFRANGOS varrem a 60/2min** (`window scan` logs) — workers vivos, recebendo áudio |
| Hashes não estão no `fingerprint_hashes` (contador cacheado mentindo) | ❌ descartado | `hashes_reais ≈ fingerprint_hash_count` (ex: 97 = 64219 ≈ 64219) |
| Master errado / é vídeo | ❌ descartado | ffprobe: MP3 limpo, ~30s. Usuário confirmou que é o anúncio certo |
| Mis-atribuição (detecta sob outra campanha) | ❌ descartado | `det_total` por `commercial_id` = 0 (não detecta em LUGAR nenhum) |
| Casa ao vivo mas não confirma | ❌ descartado | `window scan` mostra `top_score` 6-30 (nunca 50-200+ de um match real) |
| **100% `is_shared` → UniqueScore 0** | ✅ **CAUSA RAIZ** | `pct_shared = 100%` nos quebrados; ASAAS (77) = 70.2% e detecta |

**Materiais quebrados originalmente reportados** (campanhas com 0 detecção):
- short_id **93** UNIFIQUE_ENERGIA (campanhas 198/199), **96** ITAVEMA GEELY-SP (239),
  **97** NAT HINO DA COPA (241 NATFRANGOS), **102** STIHL HORTITEC (236).
- Todos fingerprintados **2026-06-09/10** (pós-#2). Todos **100% shared**.
- Controle: ASAAS **77** = 70.2% shared (re-fingerprintado 06-08, durante a migração) → **detecta**.

**Dados-chave coletados:**
```
short_id 93/96/97/102: 100.0% shared  (~60-67k hashes, TODOS is_shared)
short_id 77 (ASAAS):    70.2% shared  → 30% único → detecta
Blast radius: 37 materiais a 100%, 61 a ≥90%, de 94 ready
window scan top_score desses 4: pico ~6-8, máx 15-30 (4h), NUNCA 50-200+
```

---

## 5. PLANO DE FIX (o que executar)

### 5.1. Estratégia

A regressão é: `MinScore=5` baixo demais pra densidade do #2. **Subir o `MinScore`** pra
separar sting genuíno (pontua alto, tipo match real) do ruído denso (6-8).

**Valor:** o piso de fundo material-vs-material com #2 é ~6-8 (inferido do `window scan`
runtime; medir no sharing-scan pra cravar). Match genuíno de conteúdo compartilhado pontua
ALTO (50+). Logo `MinScore` deve ficar entre os dois — chute inicial **~15-25**. **NÃO chute
o valor final — valide empiricamente** (ver 5.4), igual fizemos com o #2.

**Recomendação de design:** tornar `MinScore` **configurável via env var** (ex.:
`SHARING_MIN_SCORE`, default novo ~20) lido tanto pelo `subscriber` (uploads novos) quanto
pelo `backfill-shared-hashes`. Assim o usuário tuna o valor **sem redeploy** — só muda a env
e re-roda o backfill. (Alternativa mais robusta, porém mais código: escalar `MinScore`
proporcional à densidade/contagem de hashes. Comece pelo configurável.)

### 5.2. Mudanças de código

1. **`workers/internal/sharing/sharing.go`**: trocar a const `MinScore = 5` por um valor lido
   de env (com default novo ~20). Cuidado: `MinScore` é usado dentro de `scanForSharedRegions`
   (a comparação `score >= MinScore` por janela). Garanta que o subscriber e o backfill usem
   o MESMO valor.
2. **TDD** (`workers/internal/sharing/sharing_test.go` — é DB-gated, `TEST_DATABASE_URL`):
   - O teste do **AMB30/JINGLE** (sting genuíno) TEM que continuar flagando o sting com o
     `MinScore` novo. ⚠️ As fixtures hoje rodam pelo pipeline #2 (denso) — confirme que o
     sting ainda pontua acima do novo threshold.
   - **Adicionar** um teste de regressão: material denso "normal" (não-subset) escaneado
     contra um catálogo denso **NÃO** pode virar ~100% flagged.
   - Disciplina: RED→GREEN, assista falhar antes (skill `superpowers:test-driven-development`).

### 5.3. Migração de dados (reset + re-backfill)

Depois do código deployado:
```bash
# 1. resetar TODOS os flags (idempotente, reversível re-rodando o backfill)
docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml \
  --env-file infra/docker/.env exec -T postgres sh -c \
  'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -c "UPDATE fingerprint_hashes SET is_shared=false WHERE is_shared=true;"'

# 2. re-rodar o backfill com o MinScore novo (lê a env)
docker compose ... exec api backfill-shared-hashes --dsn "$DATABASE_URL"
#   (regra 4.2 do CLAUDE.md: BUILD + recreate do api ANTES, senão o exec roda binário velho)
```
**Atenção (CLAUDE.md §4.2):** `docker compose exec` roda o binário do container ATUAL.
Ordem certa: `build api` → `up -d --force-recreate --no-deps api` → só então `exec ... backfill`.

### 5.4. Validação (NÃO pule — valide o valor)

Depois do re-backfill, rode a query de distribuição e confira:
```sql
SELECT count(*) FILTER (WHERE pct = 100) AS shared_100,
       count(*) FILTER (WHERE pct >= 90)  AS shared_90mais,
       count(*) FILTER (WHERE pct BETWEEN 30 AND 89) AS shared_medio,
       count(*) FILTER (WHERE pct < 30)   AS shared_baixo,
       count(*) AS total
FROM (SELECT m.id, round(100.0*count(*) FILTER (WHERE fh.is_shared)/count(*),0) AS pct
      FROM materials m JOIN fingerprint_hashes fh ON fh.commercial_id=m.id
      WHERE m.fingerprint_status='ready' GROUP BY m.id) x;
```
**Critério de aceite:**
- `shared_100` deve cair pra ~0 (ou só duplicatas reais).
- Materiais normais de 30s devem ficar **baixo** (<30%, idealmente perto de 0).
- Subsets reais (corte 30s⊂60s) ficam em 0% (protegidos pelo SubsetThreshold).
- Os 4 materiais-teste (93/96/97/102) devem **passar a detectar** (confira `det_total` por
  `commercial_id` subindo, e o `window scan` deles batendo 50+ numa veiculação).
- **Sem novo falso-positivo** (o audit §9.9 é a rede; observe).

Se ainda houver `shared_100` alto → o `MinScore` está baixo demais, sobe mais e re-backfilla.
Se sting genuíno parar de ser flagado (volta FP) → baixou demais. Itere (por isso o env var).

### 5.5. Opção de EMERGÊNCIA (enquanto valida o fix)

65% do catálogo está cego AGORA. Se precisar de detecção fluindo já, **desligue o shared-hash
temporariamente**:
```sql
UPDATE fingerprint_hashes SET is_shared = false WHERE is_shared = true;
```
Trade-off: volta o risco de falso-positivo de sting (o audit §9.9 + version-disambiguation
pegam parte). Mas TUDO passa a detectar. Pra um sistema sendo validado contra fornecedor,
65% de detecção faltando provavelmente dói mais que alguns FPs. **Reversível** (re-rodar o
backfill com o fix restaura). Decisão do usuário.

---

## 6. Playbook de queries diagnósticas (todas testadas, copy-paste)

Prefixo (defina uma vez por sessão SSH na VM):
```bash
DC="docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env"
PSQL() { $DC exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'; }
```

**shared_pct por material (o diagnóstico-chave):**
```sql
SELECT m.short_id, m.title,
       count(*) AS hashes,
       count(*) FILTER (WHERE fh.is_shared) AS compartilhados,
       round(100.0 * count(*) FILTER (WHERE fh.is_shared) / count(*), 1) AS pct_shared
FROM materials m JOIN fingerprint_hashes fh ON fh.commercial_id = m.id
WHERE m.short_id IN (77, 93, 96, 97, 102)   -- 77 = controle que detecta
GROUP BY m.short_id, m.title ORDER BY pct_shared DESC;
```

**Campanhas ativas com 0 detecção (acha os quebrados):**
```sql
SELECT c.name, c.status,
       count(DISTINCT cm.material_id) AS materiais,
       (SELECT count(*) FROM detections d WHERE d.campaign_id=c.id) AS deteccoes
FROM campaigns c LEFT JOIN campaign_materials cm ON cm.campaign_id=c.id
WHERE c.status='ativa' AND c.start_date <= (now() AT TIME ZONE 'America/Sao_Paulo')::date
GROUP BY c.id ORDER BY deteccoes ASC;
```

**Material detecta em algum lugar? (descarta mis-atribuição):**
```sql
SELECT m.short_id, (SELECT count(*) FROM detections d WHERE d.commercial_id=m.id) AS det_total
FROM materials m WHERE m.short_id IN (93,96,97,102);
```

**window scan ao vivo (worker está varrendo + com que score):** roda na VM, `--tail` é mais
rápido que `--since`:
```bash
$DC logs --since 2h api 2>&1 \
  | grep -oE '"top_commercial_short_id":(93|96|97|102),"top_score":[0-9]+' \
  | sort | uniq -c | sort -rn | head
# pico ~12 = só ruído (não casa); 50+ = casou (aí seria bug de confirmação, não shared-hash)
```

---

## 7. Infra e mecânica de deploy

- **Máquina:** `c3-highcpu-8` (8 vCPU, 16 GB), GCP southamerica-east1. Hostname `vm-e-monitor`,
  user de SSH `radiocheck` (ou `tatico3`). Uso de CPU ~36-40%.
- **Deploy de mudança Go (api/sharing/matcher):** o matching + supervisor + sharing.Subscriber
  rodam no processo `api`. Surgical:
  ```bash
  cd ~/radiocheck && git pull
  $DC build api && $DC up -d --force-recreate --no-deps api   # --no-deps obrigatório (regra 4.1)
  ```
  ⚠️ Recriar o `api` reinicia os workers (breve gap de detecção, segundos).
- **Deploy de mudança Python (fingerprint/generator/broadcast_sim):** `$DC build fingerprint &&
  $DC up -d --force-recreate --no-deps fingerprint`.
- **`./scripts/deploy.sh`** faz build+up+migrate de tudo (inclui o override file). Para mudança
  só-código sem migração, prefira o surgical acima.
- **Commits desta sessão foram direto na `master`** (o usuário pediu). `origin/master` em
  `20f9e13`. Sem `.github/workflows` → push na main NÃO faz auto-deploy de backend (só o
  frontend Cloudflare Pages, que não tocamos).
- **Ambiente local do dev é Windows** — testes Go DB-gated (`TEST_DATABASE_URL`) SKIPAM local;
  validação real é em prod ou num banco de teste. Testes Python rodam local (`cd fingerprint &&
  python -m pytest tests/ -q`).

**Regras críticas do CLAUDE.md (releia antes de mexer em prod):**
- §4.1 `--force-recreate` SEM `--no-deps` propaga pro postgres e pode apagar o `pgdata`. SEMPRE
  `--no-deps` em service stateful. (Incidente 2026-05-12 destruiu 100% do pgdata.)
- §4.2 `exec` roda binário ANTIGO até o `--force-recreate`. Build → recreate → exec.
- §5 NUNCA `npm install` em `frontend/` no Windows (poda optional deps linux do lockfile,
  quebra o CF Pages). Não relevante pra este fix, mas fique ligado.

---

## 8. Follow-ups abertos (não-bloqueantes)

1. **Backfill histórico de dupla contagem (do fix do retract `20f9e13`):** entre 2026-06-08
   18:03 e o deploy do retract, cortes de 15s que deviam ter sido retraídos ficaram `available`
   (inflando contagem vs fornecedor). Precisa de um backfill que ache 15s sobrepostos a um 30s
   do mesmo cliente/emissora/janela e marque `retracted_at`. O usuário queria isso pros números
   baterem. (Oferecido, não feito.)
2. **Recalibração de threshold pós-#2:** o `min_hashes` por estação (calibração) pode não ter
   sido recalibrado depois do #2 (a densidade subiu o noise floor). Relacionado a este incidente
   — se for fazer o `MinScore` do sharing "alinhar com o runtime", confira o `station_thresholds`
   atual primeiro. Ver [docs/operations/calibration.md].
3. **hop 1s (LW-1):** opção pra baixar o piso de detecção de ~5s pra ~3s. Discutido, não feito.
   Custo ~+12pts de CPU (cabe no 8 vCPU). Só se precisar de áudio mais curto.
4. **Índice versionado de fingerprint:** elimina a janela degradada na re-fingerprint (hoje o
   cutover é atômico/arriscado). Melhoria futura pra migrações de densidade recorrentes.

---

## 9. Estado do repositório no handoff

- `origin/master` = `20f9e13` (os 4 commits da sessão, todos pushados).
- O fix de shared-hash deste incidente **NÃO foi começado** (só leitura do código). Working tree
  limpo exceto 2 arquivos `M` pré-existentes que NÃO são desta sessão e NÃO devem ser commitados:
  `scripts/deploy.sh` e `workers/internal/match/engine_pass_bench_test.go`.
- Scripts de diagnóstico úteis (já commitados em `3bd7178`): `fingerprint/scripts/{evaluate_short_audio,
  diagnose_censura,replay_live,test_density}.py` — harnesses de validação offline de fingerprint.

---

## 10. Resumo executável pro próximo agente

1. Leia seções 1, 3, 5. A causa raiz está cravada — não re-investigue (seção 4).
2. Confirme o blast radius atual (query seção 6, "shared_pct").
3. Decida com o usuário: emergência (5.5, desliga shared-hash já) agora, ou direto pro fix.
4. Implemente o fix (5.2) com TDD — `MinScore` configurável, default ~20. RED→GREEN.
5. Deploy do `api` (seção 7), reset + re-backfill (5.3).
6. **Valide o valor** (5.4) — itere o `MinScore` via env até a distribuição ficar sã e os 4
   materiais-teste detectarem, sem novo FP. Não chute o valor final.
7. Atualize este doc com o `MinScore` final e o resultado; mova status pra `resolvido`.

**O usuário acertou ao insistir que "era nosso" — e era o #2. Trate como crítico (65% cego).**
