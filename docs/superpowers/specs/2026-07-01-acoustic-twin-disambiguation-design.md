# Desambiguação de gêmeos acústicos pelo trecho discriminante

**Data:** 2026-07-01
**Status:** design aprovado, pré-implementação
**Origem:** investigação do caso Milium (campanha 060f001c) — o matcher creditou "FESTIVAL DE INVERNO JUNHO" (mat 48) a tocadas que eram "01 a 05.07" (mat 149), gêmeos acústicos de 30s. Ver memória `milium-acoustic-twin-misattribution`.

---

## 1. Problema

Dois (ou mais) materiais do mesmo anunciante que compartilham a maior parte do áudio (mesma locução-base/jingle) e diferem só num trecho curto — a chamada, a data falada ("de 1 a 5 de julho" vs "festival de inverno junho"). Chamamos de **gêmeos acústicos**.

O fingerprint casa o **trecho compartilhado** dos dois masters, então ambos confirmam. A desambiguação atual (§18.2.2 `reattributeByCoverage`) compara a cobertura do clipe contra o **master inteiro** — que empata pros gêmeos (o corpo comum domina a conta). O desempate cai pra duração; se as durações são iguais (30s vs 30s), cai pro menor `short_id`, creditando o gêmeo errado.

**Por que o mecanismo existente não pega, com precisão:**
- `fingerprint_hashes.is_shared` marca hashes compartilhados; o matcher só confirma pelo `UniqueScore` (hits não-compartilhados). Match só no corpo comum não deveria confirmar.
- **MAS** `sharing.classifyAndFilter` ([workers/internal/sharing/sharing.go:347-382]) usa `score = max(ownCov, otherCov)`: overlap **< 0.5 = "sting" → marca shared**; overlap **≥ 0.5 = "subset/duplicata" → NÃO marca**, premissa explícita *"deixa a duração desempatar"*.
- Gêmeos de mesma duração se sobrepõem ≥0.5 → classificados "subset" → corpo comum **não marcado** → conta como Unique pros dois → ambos confirmam. E a premissa "duração desempata" quebra (mesma duração).

## 2. Princípio inegociável

**Evidência, nunca programado.** A atribuição sai do áudio que **realmente tocou**. Se a rádio veicular o criativo errado, o sistema tem que **flagrar isso**, não "corrigir" pro que estava contratado. Qualquer solução por agendamento/data é rejeitada — mascara erro de veiculação e destrói a proposta do produto ("prova, não afirmação").

Corolário: quando o áudio **não permite** decidir (trecho discriminante não sobreviveu à degradação, ou gêmeos idênticos), o sistema marca **ambíguo** e manda pra revisão. Nunca chuta, nunca inventa número.

## 3. Objetivos / Não-objetivos

**Objetivos:**
- Atribuir gêmeos de mesma duração ao criativo que de fato tocou, usando o **trecho que os distingue**.
- Preservar recall: a tocada nunca é perdida — no pior caso vira "ambíguo" (registrada, sinalizada), não some.
- Reusar a infra existente (similaridade, shared-hash/overlap, audit, `reattributeByCoverage` + `syncCanonicalProjection`).
- Não tocar no matcher em tempo real (menor risco).

**Não-objetivos:**
- Desambiguar por agendamento/data (rejeitado, §2).
- Resolver gêmeos **idênticos** de mesma duração (impossível pelo áudio — vira ambíguo; é problema de cadastro).
- Mexer no caso 15s/30s / subset de durações diferentes (já resolvido pela cobertura+duração existente).

## 4. Arquitetura — B (pós-audit)

Estende o gancho de desambiguação que **já existe** (`reattributeByCoverage`, roda pós-§9.9 quando `DISAMBIG_BY_COVERAGE` está ligado). Não altera o `MatchWindow` nem o state machine. Fluxo:

```
detecção confirmada → §9.9 audit passa → reattributeByCoverage:
  1. cobertura-cheia do clipe vs cada gêmeo candidato (comportamento atual)
  2. vencedor claro por margem (coverageMargin 1.5×)?  → reatribui (pega subset/15s-30s)
  3. EMPATE de cobertura  E  duração ~igual?           → passo discriminante
  4. discriminante decide  OU  marca ambíguo
```

**A trava (passo 3) é o coração do design.** O discriminante **só** dispara quando nem cobertura nem duração resolvem — o único caso órfão (gêmeos de mesma duração). Todo o resto (loop 30/60, subset, 15/30) morre no passo 2.

## 5. Design detalhado

### 5.1 Conjunto de gêmeos
Gêmeo-candidato de M = material com **similaridade ≥ `WarnThreshold` (0.50)** a M (reusa `similarity.CheckMaterialSimilarity`, `score = max(ownCov, otherCov)`). A similaridade só serve pra **achar candidatos** — a proteção contra subset/loop é a trava do passo 3, não o conjunto de gêmeos.

Computado quando o fingerprint de M fica `ready` (e reciprocamente quando um material similar entra depois). Persistido pra não recalcular por tocada.

### 5.2 Regiões discriminantes
Para um par de gêmeos (X, Y), reusa o cálculo de **overlap por frame-range** do `sharing` (o mesmo `MatchWindow` deslizante que já identifica regiões comuns, hoje usado pro `is_shared`). A **região discriminante de X vs Y** = frames de X **fora** do overlap com Y.

Persistência (nova tabela, **separada** do `is_shared` pra não afetar o matcher):

```sql
CREATE TABLE material_twin_discriminative (
    material_id  UUID NOT NULL,          -- material X
    twin_id      UUID NOT NULL,          -- gêmeo Y
    disc_ranges  int4range[] NOT NULL,   -- frame-ranges de X que NÃO estão em Y
    disc_frames  INT NOT NULL,           -- total de frames discriminantes (cache p/ denominador)
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (material_id, twin_id)
);
```

Bidirecional: gravamos (X,Y) e (Y,X). Se a região discriminante de X é **vazia** (X ⊂ Y, 100%), `disc_frames = 0` → sinaliza "não separável por esse par".

### 5.3 Cobertura discriminante (extensão do audit)
O `audit.runMatch` ([auditor.go:181-260]) já computa, no delta-bin vencedor, o conjunto de frames do master cobertos pelo clipe. Estende-se o auditor pra, dado um conjunto de frame-ranges, devolver a **cobertura restrita**:

```
disc_coverage(clip, X, Y) = |frames de X cobertas pelo clipe ∩ disc_ranges(X,Y)| / disc_frames(X,Y)
```

Reusa o match já rodado (intersecção pós-hoc das frames cobertas com `disc_ranges`), sem re-fingerprintar. `disc_frames = 0` → `disc_coverage` indefinida → contribui pra "ambíguo".

### 5.4 Regra de decisão (passo 3-4)
Dado o material atribuído X, sua cobertura-cheia `cov_X`, e um gêmeo Y considerado **empatado** — isto é, `hi_cov < lo_cov × coverageMargin` (nenhuma cobertura supera a outra pela margem, i.e., o passo 2 já teria caído pra duração) **E** `|dur_X − dur_Y| ≤ durTolerance`:

1. Calcula `disc_X = disc_coverage(clip, X, Y)` e `disc_Y = disc_coverage(clip, Y, X)`.
2. Se `max(disc_X, disc_Y) < discFloor` (nenhuma assinatura sobreviveu) → **ambíguo**.
3. Se `disc_Y ≥ disc_X * discMargin` → reatribui X→Y (`ReattributeDetection` + `syncCanonicalProjection`).
4. Se `disc_X ≥ disc_Y * discMargin` → mantém X (estava certo).
5. Quase-empate discriminante (nenhum supera o outro por `discMargin`) → **ambíguo**.

Constantes (tunáveis, valores iniciais a calibrar): `durTolerance = 1.0s` (ou 10%), `discFloor` ~ `DefaultMinCoverage` (0.15), `discMargin = 1.5` (mesma folga do `coverageMargin`).

Com >2 gêmeos empatados, aplica par-a-par contra o melhor rival; se qualquer par cair em ambíguo, o resultado é ambíguo.

### 5.5 Estado "ambíguo"
Novo `detections.evidence_status = 'ambiguous'` (adiciona ao CHECK). Semântica:
- **Não** entra no conjunto "aprovado" (`ApprovedDetectionsFilter`) — não conta como confirmada em grade/insights/cobrança.
- Aparece numa **fila de revisão** (nova tela/aba admin) onde o operador ouve o clipe e escolhe o gêmeo certo (ou descarta). A escolha manual re-usa `ReattributeDetection` (com `syncCanonicalProjection`) e marca `manual_*`.
- A projeção canônica acompanha (mesma regra de ouro do incidente 2026-06-30).

## 6. Bordas (validadas)

- **Loop 30s ⊂ 60s (durações diferentes):** o clipe de 30s cobre o master de 30s ~1.0 e o de 60s ~0.5 → **passo 2** separa (cobertura). Clipe de 60s empata cobertura mas a **duração** desempata (passo 2). Discriminante nunca dispara. Sem risco.
- **Subset / 15s em 30s:** idem — cobertura/duração resolvem no passo 2 (comportamento atual, intocado).
- **Gêmeos idênticos de mesma duração:** `disc_frames = 0` → sempre **ambíguo**. Correto (nem humano separa) — e sinaliza problema de cadastro.
- **Trecho discriminante perdido na compressão:** `disc_coverage < discFloor` → **ambíguo**, não miss.

## 7. Backfill
Comando `cmd/backfill-twin-discriminative` (espelha `backfill-shared-hashes`): pra cada material `ready`, computa gêmeos + regiões discriminantes e popula `material_twin_discriminative`. Idempotente.

Backfill de **detecções passadas** é **opt-in e separado** (`cmd/redisambiguate-twins`): re-roda a desambiguação discriminante sobre detecções históricas de campanhas/materiais dados, com `--dry-run` (preview) obrigatório antes de aplicar. Forward-only é o default; o backfill histórico é ferramenta pontual (o caso Milium atual já foi reparado à mão).

## 8. Observabilidade
- `metrics.MatchDisambiguation` (já existe) ganha labels: `reattributed_by_discriminative`, `ambiguous_by_discriminative`, `kept_by_discriminative`.
- Log estruturado por decisão discriminante (detection_id, X, Y, cov_X, cov_Y, disc_X, disc_Y, verdict).
- Alerta/painel: taxa de `ambiguous` por dia (subida súbita = gêmeo novo mal cadastrado ou degradação de captura).

## 9. Testes
- **Unit puro** (sem DB): a regra de decisão (§5.4) — tabela de casos: empate+discriminante-claro→reatribui; empate+disc-vazio→ambíguo; cobertura-clara→passo 2 (não chama discriminante); loop 30/60→passo 2.
- **Cálculo de região discriminante** (§5.2) contra fixtures de overlap conhecido.
- **Integração (DB limpo)**: seed de 2 masters gêmeos (fixtures de fingerprint com trecho comum + trecho único), clipe sintético de cada, verifica atribuição correta + ambíguo quando o trecho único é zerado.
- **Regressão**: os testes existentes de `chooseByCoverage`/`reattributeByCoverage`/15s-30s continuam verdes (o passo 2 é intocado).
- Rodar em **DB limpo migrado** (não o dev populado) — padrão do projeto (memória `detection-campaigns-projection-sync-reattribution`).

## 10. Rollout
- Gated por env var `DISAMBIG_TWIN_DISCRIMINATIVE` (default OFF), independente do `DISAMBIG_BY_COVERAGE`. Sobe desligado; liga após validar métricas em sombra.
- Migrations aditivas (nova tabela + valor no CHECK de evidence_status) — passam no shadow-test do deploy.
- Cross-compile linux + testes em DB limpo antes do push (regra 6 do CLAUDE.md).

## 11. Riscos e mitigação
- **Custo de re-audit por gêmeo:** limitado (só quando há empate de cobertura + gêmeo de mesma duração — raro; a maioria das tocadas não tem gêmeo). Best-effort, nunca bloqueia upload.
- **Calibração de `discFloor`/`discMargin`:** começar conservador (favorece "manter atribuição atual"/ambíguo a reatribuir errado); calibrar com dados reais em sombra antes de ligar.
- **Fila de revisão vira gargalo se muitos ambíguos:** monitorar a taxa; ambíguo alto = sinal de cadastro (dois materiais quase idênticos) a resolver na origem.
- **Interação com multi-atribuição (F-119):** a reatribuição usa `ReattributeDetection` que já sincroniza a projeção canônica (fix 2026-06-30). Projeções de fan-out de outras campanhas ficam intactas.

## 12. Arquivos-âncora (implementação)
- `workers/internal/similarity/similarity.go` — conjunto de gêmeos.
- `workers/internal/sharing/sharing.go` — cálculo de overlap → regiões discriminantes (novo `ComputeTwinDiscriminative`).
- `workers/internal/audit/auditor.go` — cobertura restrita a frame-ranges (novo parâmetro/função).
- `workers/internal/evidence/service.go` + novo arquivo `twin_disambig.go` — a regra de decisão (§5.4) plugada no `reattributeByCoverage`.
- `workers/internal/catalog/detections.go` — `evidence_status='ambiguous'`, fila de revisão.
- `migrations/00NN_material_twin_discriminative.up.sql`, `00NN_evidence_status_ambiguous.up.sql`.
- `workers/cmd/backfill-twin-discriminative/`, `workers/cmd/redisambiguate-twins/`.
- Frontend: fila de revisão de ambíguos (nova aba admin).
