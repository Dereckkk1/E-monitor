# Radiocheck — Guia para Claude

Sistema de monitoramento de veiculação de comerciais em rádios AM/FM via streaming, substituindo fornecedor externo. Veja o plano completo em [plano_implementacao.md](plano_implementacao.md).

---

## Regras Críticas

### 1. O plano é lei — mas pode ser alterado com justificativa

O arquivo `plano_implementacao.md` é a fonte única de verdade arquitetural deste projeto. Ao desenvolver qualquer coisa:

- **Consulte o plano antes de começar.** Use o índice abaixo para ir direto à seção relevante sem ler o arquivo inteiro.
- **Não fuja do escopo silenciosamente.** Se perceber que a implementação vai em direção diferente do plano, pare e avise.
- **O plano é maleável**, mas só deve ser alterado quando uma solução melhor justificar a mudança. Antes de implementar diferente do que está escrito: defenda a tese, explique o porquê vale a pena mudar, aponte qual seção deve ser atualizada, e só então implemente.

### 2. Documentação de novas funcionalidades vai em `/docs`

**Nunca documente funcionalidades novas no `plano_implementacao.md`.** Esse arquivo é blueprint arquitetural, não documentação operacional. Toda funcionalidade implementada deve ter sua documentação oficial criada em `/docs`. Se a pasta não existir, crie-a.

### 3. Não invente escopo

Os **Não Objetivos** (seção 1.3, linha 32) definem explicitamente o que está fora do sistema. Não adicione reconhecimento de música, transcrição, integração com sistemas de tráfego ou captura over-the-air — mesmo que pareça útil. Qualquer expansão de escopo precisa de aprovação explícita do usuário.

### 4. Operações destrutivas em prod — leitura obrigatória antes de mexer

Esta seção existe por causa do **incidente 2026-05-12** ([postmortem](docs/incidents/incident-2026-05-12-pgdata-loss.md)) que destruiu 100% do `pgdata` de produção. Regras:

**4.1. `--force-recreate` sem `--no-deps` é proibido em produção.**

`docker compose up -d --force-recreate <service>` propaga o recreate para as dependências declaradas em `depends_on`. Para `api`, isso inclui `postgres`. Em determinadas condições o Docker considera o volume divergente e o recria do zero — apagando o dado.

```bash
# ❌ NUNCA em prod:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate api

# ✅ CORRETO:
docker compose -f infra/docker/docker-compose.yml up -d --force-recreate --no-deps api
```

A mesma regra vale para `down`, `restart`, `up`: se está mexendo em qualquer service stateful (postgres/minio/redis), passe `--no-deps` quando aplicável.

**4.2. `docker compose exec` NÃO usa imagem nova — recreate antes.**

`docker compose build api` produz imagem nova mas **não troca o binário no container que está rodando**. `docker compose exec api ...` continua executando o binário antigo. Ordem correta de deploy:

```bash
docker compose build api                                              # 1. build da imagem
docker compose up -d --force-recreate --no-deps api                   # 2. recreate (PEGA imagem nova)
docker compose exec api backfill-shared-hashes --dsn "$DATABASE_URL"  # 3. comandos one-shot
```

Inverter a ordem (2 e 3) faz o comando rodar com binário velho silenciosamente. Foi o que aconteceu com o F-108 v2 em prod no incidente 2026-05-12 — backfill rodou antigo.

**4.3. Antes de testar comando experimental em prod: teste local primeiro.**

Você tem dev local (Windows). Use. Não rode comando inédito direto na VM.

**4.4. Volumes de dado crítico usam bind mount no host, não volume nomeado.**

Em prod, o `.env` da VM deve ter `PGDATA_HOST_PATH`, `MINIODATA_HOST_PATH` e `MASTERSDATA_HOST_PATH` apontando para paths em `/srv/radiocheck/*`. Bind mounts sobrevivem a `docker compose down -v` e `--force-recreate`. Detalhes: [docs/operations/data-durability.md](docs/operations/data-durability.md).

**4.5. Backup precisa ser testado end-to-end, não só configurado.**

O backup do incidente 2026-05-12 estava "configurado" há 4 dias mas **nunca tinha rodado uma vez com sucesso** — falhas silenciosas no entrypoint engoliam o erro. Antes de confiar em qualquer backup, valide que o arquivo realmente aparece no destino:

```bash
# Após qualquer mudança no backup: rode manualmente e confirme upload
docker compose exec backup sh /backup.sh
docker run --rm -e AWS_ACCESS_KEY_ID="$R2_ACCESS_KEY" \
  -e AWS_SECRET_ACCESS_KEY="$R2_SECRET_KEY" \
  amazon/aws-cli --endpoint-url "$R2_ENDPOINT" \
  s3 ls "s3://$R2_BUCKET/postgres/daily/" | tail -3
```

**4.6. NUNCA monte `migrations/` em `docker-entrypoint-initdb.d` + use `migrate` service simultaneamente.**

O incidente 2026-05-12 expôs esse conflito latente: `initdb.d` executa `.sql` em ordem alfabética (incluindo `.down.sql` ANTES de `.up.sql`), deixando o schema em estado fragmentado que o golang-migrate marca como `dirty` e se recusa a continuar. Resultado: `users` table não criada → api crash loop. Recovery exige `DROP SCHEMA public CASCADE` + re-run do migrate. Limpeza definitiva em F-111 (pendente — remover o mount do `initdb.d`).

**4.7. Prod usa `docker-compose.override.yml` (gitignored) — sempre incluir em comandos manuais.**

A VM tem `infra/docker/docker-compose.override.yml` com bind mounts para `/mnt/db/pgdata`, `/mnt/data/minio`, `/mnt/data/masters` e `/mnt/data/audio-refs`. O `scripts/deploy.sh` em prod sempre inclui esse arquivo. **Comandos manuais sem o override usam configuração diferente da rodando — silenciosamente.** Isso causou ~4h de caos no incidente 2026-05-12: rodamos diagnóstico apontado pro `pgdata` named volume vazio enquanto o dado real estava intacto em `/mnt/db/pgdata`. Detalhes em [incident-2026-05-12-pgdata-loss.md §Causa raiz #5](docs/incidents/incident-2026-05-12-pgdata-loss.md).

```bash
# ❌ CONFIGURAÇÃO DIFERENTE da prod (silenciosamente):
docker compose -f infra/docker/docker-compose.yml ps

# ✅ Espelha o que o deploy.sh usa:
docker compose -f infra/docker/docker-compose.yml \
               -f infra/docker/docker-compose.override.yml \
               --env-file infra/docker/.env ps

# ✅✅ Atalho — só roda o deploy.sh, que já faz isso:
./scripts/deploy.sh
```

Após F-116 (base yml suporta `PGDATA_HOST_PATH` via env vars), o override é redundante e será removido em limpeza futura. Por enquanto continua existindo na VM por compatibilidade.

### 5. `npm install` no Windows quebra o build do Cloudflare Pages — leitura obrigatória antes de mexer em deps do frontend

Esta seção existe porque o erro **se repete**: rodar `npm install`/`npm i <pkg>` em `frontend/` no Windows **poda do `package-lock.json` as dependências opcionais específicas de Linux** (ex.: `@emnapi/*`, bindings nativos que o `vite`/`rolldown` usa no build). O Cloudflare Pages roda **`npm ci` em Linux**, que exige o lockfile completo e falha com:

```
npm error code EUSAGE
npm error `npm ci` can only install packages when your package.json and
npm error package-lock.json ... are in sync.
npm error Missing: @emnapi/core@x.y.z from lock file
```

**5.1. Sintoma.** Deploy do CF Pages morre no passo `npm clean-install` logo após instalar o Node. Quase sempre depois de um commit que tocou `frontend/package.json` + `package-lock.json`.

**5.2. Causa raiz.** `npm` no Windows reescreve o lockfile sem os pacotes `os: ["linux"]` opcionais. Lockfile v3 *deveria* preservá-los cross-platform, mas na prática o install no Windows os remove. Os bindings nativos do toolchain de build (rolldown/vite 8) são linux-only em prod.

**5.3. Como adicionar/atualizar uma dep do frontend sem quebrar o CF Pages.** NÃO commite um lockfile podado. Reconstrua a partir do lockfile completo (master) injetando só a dep nova, e valide:

```bash
# 1. parte do lockfile completo do master e injeta SÓ a dep nova (node script)
node -e "
const cp=require('child_process'), fs=require('fs');
const head=JSON.parse(cp.execSync('git show HEAD:frontend/package-lock.json',{maxBuffer:1e8}));
const base=JSON.parse(cp.execSync('git show master:frontend/package-lock.json',{maxBuffer:1e8}));
const k='node_modules/<PKG>';
base.packages[k]=head.packages[k];
base.packages[''].dependencies['<PKG>']=head.packages[''].dependencies['<PKG>'];
const o={};Object.keys(base.packages).sort().forEach(x=>o[x]=base.packages[x]);base.packages=o;
fs.writeFileSync('frontend/package-lock.json',JSON.stringify(base,null,2)+'\n');
"

# 2. valida a sincronia (é o check exato que o CF roda). exit 0 = ok.
cd frontend && npm ci --dry-run

# 3. confirma que as opcionais linux continuam no lockfile
grep -c emnapi frontend/package-lock.json   # deve bater com o do master, não cair
```

**5.4. Verificação rápida antes de qualquer push que toque o lockfile.** `git show master:frontend/package-lock.json | grep -c emnapi` vs `grep -c emnapi frontend/package-lock.json` — se o número **caiu**, o lockfile foi podado: NÃO pushe, refaça pelo passo 5.3. Detalhe do incidente: ver o commit que restaurou `@emnapi` (`fix(frontend): restaura optional deps @emnapi no lockfile`).

**5.5. Alternativa definitiva (quando der pra testar).** Rodar o `npm install` num ambiente Linux (WSL/container) gera o lockfile completo de uma vez. Enquanto não houver esse fluxo, use 5.3.

---

## Índice do Plano (`plano_implementacao.md`)

Use estes links para ir direto à seção relevante em vez de ler o arquivo inteiro.

| Seção | Linha | Conteúdo |
|-------|-------|----------|
| [§1 Visão Geral](plano_implementacao.md#L11) | 11 | Contexto, objetivos, não-objetivos, métricas de sucesso |
| [§1.1 Contexto de Negócio](plano_implementacao.md#L13) | 13 | Por que o projeto existe, o que substitui |
| [§1.2 Objetivos Funcionais](plano_implementacao.md#L19) | 19 | Lista dos 8 requisitos funcionais do sistema |
| [§1.3 Não Objetivos](plano_implementacao.md#L32) | 32 | **Leia antes de adicionar qualquer coisa nova** |
| [§1.4 Métricas de Sucesso](plano_implementacao.md#L41) | 41 | KPIs que definem quando o projeto é bem-sucedido |
| [§2 Glossário](plano_implementacao.md#L53) | 53 | Fingerprint, constellation map, peak pair, LUFS, SBR etc. |
| [§3 Riscos Técnicos](plano_implementacao.md#L81) | 81 | Catálogo de R1–R37: todos os riscos mapeados |
| [§3.1 Riscos de Áudio Broadcast](plano_implementacao.md#L85) | 85 | R1–R10: compressão, clipping, codecs, AM |
| [§3.2 Riscos de Transporte](plano_implementacao.md#L107) | 107 | R11–R17: quedas, dropouts, mudança de URL |
| [§3.3 Riscos de Detecção](plano_implementacao.md#L123) | 123 | R18–R25: falsos positivos/negativos |
| [§3.4 Riscos Operacionais](plano_implementacao.md#L141) | 141 | R26–R37: memory leak, cooldown, storage |
| [§4 Mitigações](plano_implementacao.md#L169) | 169 | Como cada risco é endereçado |
| [§5 Arquitetura de Alto Nível](plano_implementacao.md#L229) | 229 | Componentes, fluxo de dados, deployment |
| [§5.1 Visão de Componentes](plano_implementacao.md#L231) | 231 | Diagrama textual dos componentes |
| [§5.2 Fluxo de Dados](plano_implementacao.md#L249) | 249 | Como os dados fluem entre componentes |
| [§5.3 Comunicação entre Componentes](plano_implementacao.md#L310) | 310 | Protocolos internos |
| [§5.4 Modelo de Deployment](plano_implementacao.md#L322) | 322 | Topologia de servidores |
| [§6 Stack Tecnológico](plano_implementacao.md#L420) | 420 | Linguagens, frameworks, bibliotecas — com justificativas |
| [§6.1 Stack Definitiva](plano_implementacao.md#L334) | 334 | Decisões fechadas de tecnologia |
| [§6.2 Justificativas](plano_implementacao.md#L356) | 356 | Por que cada escolha foi feita |
| [§6.3 Bibliotecas Externas](plano_implementacao.md#L386) | 386 | Libs específicas usadas |
| [§7 Pipeline de Fingerprint](plano_implementacao.md#L420) | 420 | Geração de fingerprint acústico do master |
| [§7.1 Fluxo Geral](plano_implementacao.md#L424) | 424 | Passos do pipeline |
| [§7.2 Comandos Concretos](plano_implementacao.md#L461) | 461 | Comandos ffmpeg exatos |
| [§7.3 Algoritmo de Fingerprint](plano_implementacao.md#L510) | 510 | Constellation map, peak pairs, hashing |
| [§7.4 Persistência](plano_implementacao.md#L601) | 601 | Como fingerprints são armazenados |
| [§7.5 Validação Pós-Geração](plano_implementacao.md#L609) | 609 | Como validar que o fingerprint gerado é bom |
| [§8 Pipeline de Ingestão](plano_implementacao.md#L621) | 621 | Stream worker: captura e análise de stream |
| [§8.1 Estrutura do Worker](plano_implementacao.md#L623) | 623 | Arquitetura interna do worker |
| [§8.2 Comando ffmpeg](plano_implementacao.md#L653) | 653 | Comando exato de ingestão |
| [§8.3 Ring Buffers](plano_implementacao.md#L682) | 682 | Buffers para evidência e análise |
| [§8.4 Pré-processamento](plano_implementacao.md#L690) | 690 | Processamento da janela antes do matching |
| [§8.5 Health Monitoring](plano_implementacao.md#L713) | 713 | Como detectar worker travado |
| [§8.6 Reconexão](plano_implementacao.md#L723) | 723 | Backoff exponencial e jitter |
| [§8.7 Restart Preventivo](plano_implementacao.md#L744) | 744 | Reinício periódico para mitigar memory leaks |
| [§9 Algoritmo de Matching](plano_implementacao.md#L751) | 751 | Matching acústico: o coração do sistema |
| [§9.1 Visão Geral](plano_implementacao.md#L755) | 755 | Overview do algoritmo |
| [§9.2 Índice em Memória](plano_implementacao.md#L768) | 768 | Estrutura de dados do índice de hashes |
| [§9.3 Match por Janela](plano_implementacao.md#L795) | 795 | Histograma de delta, algoritmo completo |
| [§9.4 Threshold Adaptativo](plano_implementacao.md#L871) | 871 | Calibração por emissora |
| [§9.5 State Machine](plano_implementacao.md#L891) | 891 | Máquina de estados de detecção |
| [§9.6 Cobertura Temporal](plano_implementacao.md#L928) | 928 | Cálculo de cobertura para confirmação |
| [§9.7 Matching Multi-Rate](plano_implementacao.md#L950) | 950 | Tolerância a time stretching (R8) |
| [§9.8 Desambiguação de Versões](plano_implementacao.md#L962) | 962 | Escolha entre cortes do mesmo comercial (R20) |
| [§9.9 Audit de Evidência Pré-Persist](plano_implementacao.md#L979) | 979 | Re-fingerprint do clipe salvo × master antes de marcar veiculação como confirmada |
| [§10 Verificação Neural](plano_implementacao.md#L1037) | 1037 | Camada de verificação por embeddings neurais |
| [§10.1 Quando Ativar](plano_implementacao.md#L985) | 985 | Condições para uso da camada neural |
| [§10.2 Modelo](plano_implementacao.md#L995) | 995 | Modelo de embedding usado |
| [§10.3 Pipeline](plano_implementacao.md#L1001) | 1001 | Pipeline de verificação neural |
| [§11 Geração de Evidência](plano_implementacao.md#L1033) | 1033 | Gravação e armazenamento de clips de evidência |
| [§11.1 Fluxo](plano_implementacao.md#L1035) | 1035 | Como a evidência é gerada |
| [§11.2 Encoding Final](plano_implementacao.md#L1048) | 1048 | Comando de encoding do clip |
| [§11.3 Layout no Storage](plano_implementacao.md#L1062) | 1062 | Estrutura de pastas/buckets |
| [§11.4 Retenção e Tiering](plano_implementacao.md#L1079) | 1079 | Política de 30 dias hot / 12 meses cold |
| [§11.5 Tratamento de Falhas](plano_implementacao.md#L1087) | 1087 | O que fazer quando o clip não salva |
| [§12 Esquema de Dados](plano_implementacao.md#L1097) | 1097 | DDL PostgreSQL completo |
| [§12.1 DDL Completo](plano_implementacao.md#L1099) | 1099 | Tabelas, índices, constraints |
| [§12.2 Estimativas de Volume](plano_implementacao.md#L1358) | 1358 | Projeção de crescimento dos dados |
| [§13 Contratos de API](plano_implementacao.md#L1371) | 1371 | API REST e eventos NATS |
| [§13.1 API Externa (Clientes)](plano_implementacao.md#L1373) | 1373 | Endpoints para integração de clientes |
| [§13.2 API Interna (Operadores)](plano_implementacao.md#L1493) | 1493 | Endpoints de operação |
| [§13.3 Eventos NATS](plano_implementacao.md#L1504) | 1504 | Mensageria interna |
| [§14 Infraestrutura](plano_implementacao.md#L1536) | 1536 | Topologia de servidores, networking, backup |
| [§14.1 Topologia](plano_implementacao.md#L1538) | 1538 | Layout de servidores recomendado |
| [§14.2 Distribuição de Workers](plano_implementacao.md#L1569) | 1569 | Como distribuir workers entre servidores |
| [§14.3 Networking](plano_implementacao.md#L1573) | 1573 | Rede, IPs, pool para anti-bloqueio |
| [§14.4 Backup e DR](plano_implementacao.md#L1581) | 1581 | Estratégia de backup e disaster recovery |
| [§14.5 Provisionamento](plano_implementacao.md#L1600) | 1600 | Configuração de servidores |
| [§15 Observabilidade](plano_implementacao.md#L1622) | 1622 | Métricas, logs, tracing, dashboards, alertas |
| [§15.1 Métricas Prometheus](plano_implementacao.md#L1624) | 1624 | Lista de métricas expostas |
| [§15.2 Logging](plano_implementacao.md#L1659) | 1659 | Estratégia de logging estruturado |
| [§15.3 Tracing](plano_implementacao.md#L1666) | 1666 | Distributed tracing |
| [§15.4 Dashboards Grafana](plano_implementacao.md#L1677) | 1677 | Painéis de monitoramento |
| [§15.5 Alertas](plano_implementacao.md#L1697) | 1697 | Regras de alerta crítico |
| [§15.6 Runbooks](plano_implementacao.md#L1714) | 1714 | Procedimentos de resposta a incidentes |
| [§16 Segurança](plano_implementacao.md#L1736) | 1736 | Auth, roles, proteção de dados, audit log |
| [§17 Migração e Coexistência](plano_implementacao.md#L1797) | 1797 | Convivência com o fornecedor atual |
| [§18 Plano de Execução por Fases](plano_implementacao.md#L1845) | 1845 | Cronograma: 4 fases ao longo de 36 semanas |
| [§18.1 Fase 1 — Prova Técnica](plano_implementacao.md#L1847) | 1847 | Semanas 1–6: PoC funcional |
| [§18.2 Fase 2 — Hardening](plano_implementacao.md#L1873) | 1873 | Semanas 7–18: estabilização e coexistência |
| [§18.2.1 Ciclo de vida de campanha](plano_implementacao.md#L1893) | 1893 | Estados programada/ativa/concluida/cancelada com transição automática por data |
| [§18.2.2 Desambiguação de versões](plano_implementacao.md#L1970) | 1970 | Dedup pós-confirmação entre cortes 30s/60s do mesmo cliente |
| [§18.3 Fase 3 — Escala](plano_implementacao.md#L1892) | 1892 | Semanas 19–30: migração comercial |
| [§18.4 Fase 4 — Corte Total](plano_implementacao.md#L1908) | 1908 | Semanas 31–36: desligamento do fornecedor |
| [§18.5 Cronograma Sumário](plano_implementacao.md#L1922) | 1922 | Tabela resumida |
| [§18.6 Go/No-Go](plano_implementacao.md#L1934) | 1934 | Marcos de decisão entre fases |
| [§19 Validação](plano_implementacao.md#L1945) | 1945 | Testes unitários, integração, carga, chaos |
| [§20 Estimativa de Custos](plano_implementacao.md#L2009) | 2009 | Infraestrutura, equipe, comparativo com fornecedor |
| [§21 Riscos do Projeto](plano_implementacao.md#L2065) | 2065 | Riscos de prazo, técnicos, regulatórios |
| [§22 Decisões Pendentes](plano_implementacao.md#L2120) | 2120 | Decisões tomadas, premissas, itens aguardando input |
| [§23 Referências](plano_implementacao.md#L2158) | 2158 | Material de estudo e papers |
| [§24 Apêndice A — ffmpeg](plano_implementacao.md#L2174) | 2174 | Comandos ffmpeg de referência |
| [§25 Apêndice B — Constantes](plano_implementacao.md#L2234) | 2234 | Tabela de constantes do sistema |
| [§26 Apêndice C — Estrutura de Repositório](plano_implementacao.md#L2276) | 2276 | Layout de pastas esperado |
| [§27 Conclusão e Próximos Passos](plano_implementacao.md#L2401) | 2401 | Próximas ações e critério de sucesso final |

---

## Contexto Rápido

- **Problema:** substituir fornecedor de monitoramento de comerciais em rádio que custa caro e tem atendimento ruim.
- **Meta principal:** monitorar 200+ emissoras simultaneamente, detectar comerciais em <10s, com 98%+ de concordância com o fornecedor atual.
- **Núcleo técnico:** fingerprinting acústico estilo Shazam (constellation map + histogram de delta), com broadcast simulation na referência para robustez a degradação de stream.
- **Fase atual:** ver §18 para o cronograma. Fase 1 é PoC técnico (semanas 1–6).
- **Stack:** ver §6. Decisões fechadas; não sugira trocar sem justificativa forte.

---

## Documentação em `/docs` — formato obrigatório

A documentação operacional do sistema fica em `/docs`. Estrutura canônica:

```
docs/
  README.md                 — índice (mantenha atualizado ao adicionar/mover docs)
  AUDIT-AAAA-MM-DD.md       — relatórios de auditoria (preservar histórico, não sobrescrever)
  architecture/             — como o sistema funciona (conceitual, estável)
  features/                 — feature implementada (uma feature por arquivo)
  operations/               — operar o sistema em prod
  incidents/                — postmortems (incident-AAAA-MM-DD-{slug}.md)
  roadmap/                  — follow-ups, evaluations, planos de fase
  archive/                  — histórico inativo (não mover de volta sem motivo forte)
  runbooks/                 — resposta a alertas Prometheus
  superpowers/              — specs/plans (gerados por skill — não editar à mão)
```

### Regras

1. **Header YAML obrigatório no topo de todo doc** (exceto README/AUDIT/superpowers):

   ```yaml
   ---
   status: implementado | parcialmente-implementado | legado | planejado
   ultima-verificacao: AAAA-MM-DD
   codigo-relacionado:
     - workers/internal/foo/bar.go
     - migrations/00NN_xxx.up.sql
   ---
   ```

2. **Ao implementar feature nova:** criar `docs/features/{nome}.md` com o header. Se a feature for arquitetural (algoritmo, fluxo de dados), usar `docs/architecture/`. Se for operacional (deploy, observabilidade), usar `docs/operations/`.

3. **Ao resolver incidente:** criar `docs/incidents/incident-AAAA-MM-DD-{slug}.md` com postmortem, ações pós-incidente e referências cruzadas.

4. **NUNCA documente feature nova no `plano_implementacao.md`.** Esse arquivo é blueprint arquitetural, não changelog operacional.

5. **Atualizar `ultima-verificacao`** quando revalidar o doc contra o código.

6. **Status flutua:** `implementado` → `legado` quando o código diverge significativamente; sinalize com comentário inline e crie follow-up para reescrever.

### Mapa de consulta — quando trabalhar em X, ver Y

| Quando você for mexer em… | Comece por |
|---------------------------|------------|
| Schema / migrations | [docs/operations/migrations.md](docs/operations/migrations.md) — leitura obrigatória antes de mexer em schema |
| Auth, JWT, bootstrap admin, role gating | [docs/operations/auth-bootstrap.md](docs/operations/auth-bootstrap.md) |
| Gerenciamento de usuários (admin/cliente, /admin/users, /account) | [docs/features/user-management.md](docs/features/user-management.md) |
| Desativar cliente (reversível) + delete→409 com vínculos + login gating de cliente inativo | [docs/features/client-deactivation.md](docs/features/client-deactivation.md) |
| Deploy, docker-compose, override file, Cloudflare Tunnel | [docs/operations/deploy.md](docs/operations/deploy.md) |
| Disco cheio na VM / segmentos de evidência (`segmentsdata`) ocupando o disco de OS | [docs/operations/segments-disk-migration.md](docs/operations/segments-disk-migration.md) |
| Backup, restore, retenção de evidência | [docs/operations/data-durability.md](docs/operations/data-durability.md) (canônico) + [docs/operations/backup-and-retention.md](docs/operations/backup-and-retention.md) (parcialmente desatualizado) |
| OpenTelemetry, Jaeger, spans, sampling | [docs/operations/tracing.md](docs/operations/tracing.md) |
| Profiling com pprof (CPU, heap, goroutines, mutex) | [docs/operations/profiling.md](docs/operations/profiling.md) |
| Dashboards Grafana, métricas | [docs/operations/dashboards.md](docs/operations/dashboards.md) |
| Calibração de threshold | [docs/operations/calibration.md](docs/operations/calibration.md) + [docs/operations/threshold-dynamic.md](docs/operations/threshold-dynamic.md) |
| Worker reconciler (stream_url + comerciais) + stall watchdog | [docs/operations/worker-commercial-reconciler.md](docs/operations/worker-commercial-reconciler.md) (incidentes 2026-05-08 e 2026-05-15) |
| Algoritmo de fingerprint / pipeline offline | [docs/architecture/fingerprint-pipeline.md](docs/architecture/fingerprint-pipeline.md) |
| Algoritmo `shared-hash` (subset vs sting vs skip <10s) | [docs/architecture/shared-hash-detection.md](docs/architecture/shared-hash-detection.md) (incidentes 2026-05-09/12) |
| Ciclo de vida de campanha (programada/ativa/concluida/cancelada) | [docs/architecture/campaign-lifecycle.md](docs/architecture/campaign-lifecycle.md) |
| Dedup pós-confirmação entre cortes 30s/60s | [docs/architecture/version-disambiguation.md](docs/architecture/version-disambiguation.md) |
| Regras de distribuição + categorização de detection | [docs/architecture/distribution-rules.md](docs/architecture/distribution-rules.md) |
| Audit de evidência pré-persist (§9.9) | [docs/architecture/evidence-audit.md](docs/architecture/evidence-audit.md) |
| Segmentos ADTS-AAC e extração de evidência | [docs/architecture/evidence-segments.md](docs/architecture/evidence-segments.md) |
| Design tokens, CSS, .btn, RSelect, .field | [docs/architecture/frontend-design-system.md](docs/architecture/frontend-design-system.md) |
| Wizard de campanha (6 etapas) | [docs/features/campaign-wizard.md](docs/features/campaign-wizard.md) |
| CPM fixo opcional por campanha (Step 6 do wizard, override em /campaigns + /insights + dashboard) | [docs/features/campaign-fixed-cpm.md](docs/features/campaign-fixed-cpm.md) |
| Etapa Conexão do wizard (Step 3 — testar/trocar stream_url por emissora; ping/stream/worker efêmeros) | [docs/features/campaign-connection-step.md](docs/features/campaign-connection-step.md) |
| Página /detections (grade station × material × dia) | [docs/features/detections-view.md](docs/features/detections-view.md) |
| Página /materials (materiais tocáveis por campanha + Σ programado por emissora, admin + cliente) | [docs/features/materials-page.md](docs/features/materials-page.md) |
| Webhooks (HMAC, retry, outbox) | [docs/features/webhooks.md](docs/features/webhooks.md) |
| Presigned URLs (frontend acessa evidência direto no S3) | [docs/features/evidence-presigned-urls.md](docs/features/evidence-presigned-urls.md) |
| Busca de emissoras (tokens AND, field OR) | [docs/features/broadcaster-search.md](docs/features/broadcaster-search.md) |
| Geocoding de emissoras (lat/long por cidade+UF, dataset IBGE, backfill) | [docs/features/geocoding-emissoras.md](docs/features/geocoding-emissoras.md) |
| Biblioteca de materiais por cliente | [docs/features/material-library.md](docs/features/material-library.md) |
| Pipeline polimórfico (material_id OR commercial_id) | [docs/features/material-fingerprint-pipeline.md](docs/features/material-fingerprint-pipeline.md) |
| Alerta de duplicata por similaridade ≥50% | [docs/features/material-similarity-warning.md](docs/features/material-similarity-warning.md) |
| Faixa horária em overrides (popover do grid, categorizador) | [docs/features/override-time-window.md](docs/features/override-time-window.md) |
| Simulador de stream pra teste local | [docs/operations/simulacao-radio.md](docs/operations/simulacao-radio.md) |
| Página `/operations` (supervisor ao vivo — bytes, reconnects, stall restarts, min_hashes por worker) | [docs/features/operations-page.md](docs/features/operations-page.md) |
| Painel admin `/admin/overview` (health de toda a stack: infra + workers + streams + pipeline) | [docs/features/admin-system-overview.md](docs/features/admin-system-overview.md) |
| Visão Gerencial `/management` (painel admin da operação inteira — KPIs cross-campanha + mapa + feed global ao vivo, filtros opcionais) | [docs/features/management-overview.md](docs/features/management-overview.md) |
| Painel admin `/admin/monitoring` (telemetria HTTP por rota, IP × usuário com risco, bloqueio de IP/usuário, Web Vitals) | [docs/features/admin-monitoring.md](docs/features/admin-monitoring.md) |
| Painel admin `/admin/station-failures` (emissoras com falha no dia + campanhas com slots perdidos, deep-link pra /campaigns) | [docs/features/admin-station-failures.md](docs/features/admin-station-failures.md) |
| Modo "Por campanha" de `/admin/station-failures` (cards por campanha + drill-in drawer + PDF de cobrança) | [docs/features/admin-campaign-failures.md](docs/features/admin-campaign-failures.md) |
| Sininho de notificações (`/dashboard` admin, last-7d com read-state persistido) | [docs/features/admin-notifications.md](docs/features/admin-notifications.md) |
| Emails diários de alerta de campanha (iniciando sem material / iniciando / terminando — janela dias úteis, SMTP Workspace, dedup por dia) | [docs/features/campaign-notification-emails.md](docs/features/campaign-notification-emails.md) |
| Tela de login (`/login`) — hero cinematográfico, layout split, fluxo de auth | [docs/features/login-page.md](docs/features/login-page.md) |
| Relatórios de campanha (CSV consolidado/detalhado, PDF com logo E-monitor) | [docs/features/campaign-reports.md](docs/features/campaign-reports.md) |
| Dashboard de veiculação (`/insights`, admin + cliente, KPIs + 4 charts + export PNG/PDF) | [docs/features/insights-dashboard.md](docs/features/insights-dashboard.md) |
| Mapa ao Vivo (`/live-map`, admin + cliente — emissoras monitoradas pulsando no mapa do Brasil + feed de veiculações em tempo real) | [docs/features/live-map.md](docs/features/live-map.md) |
| Dívida técnica Fase 2 (F-01..F-121) | [docs/roadmap/follow-ups-fase2.md](docs/roadmap/follow-ups-fase2.md) |
| Avaliação E2E do matcher (recomendações 4.x) | [docs/roadmap/detection-evaluation-report.md](docs/roadmap/detection-evaluation-report.md) |
| Comparar detecções com o fornecedor externo / investigar "miss" | [docs/operations/vendor-reconciliation.md](docs/operations/vendor-reconciliation.md) |
| Responder a alerta Prometheus disparado | [docs/runbooks/README.md](docs/runbooks/README.md) (índice por alerta) |
| Postmortem de incidente passado | [docs/incidents/](docs/incidents/) (incident-AAAA-MM-DD-*.md) |

Antes de afirmar "vou consultar X", verifique no header YAML do doc se `status` é `implementado`. Se for `legado` ou `parcialmente-implementado`, o doc é ponto de partida, mas confirme no código antes de agir.

### Estado das branches stacked

Em **2026-05-12 fim do dia**, todas as 4 branches que estavam stacked durante o incidente foram **mergeadas em `master`**: `fix/backup-actually-runs`, `fix/postgres-bind-mount`, `fix/sharing-bidirectional-subset`, `feat/campaign-wizard-foundations`. `origin/master` HEAD em `5937cc0` (ou além — verificar com `git log -1`).

Se você é um agente futuro entrando no projeto, rode `git log --all --not master` para listar o que está fora do master atual. Snapshots históricos: [docs/incidents/state-2026-05-12.md](docs/incidents/state-2026-05-12.md).
