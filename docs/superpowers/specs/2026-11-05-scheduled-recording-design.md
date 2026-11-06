# Programar gravação — design

**Data:** 2026-11-05
**Branch:** `feat/programar-gravacao`
**Status:** aguardando revisão do Dereck

## 1. Por que existe

A E-mídias pagou uma entrevista numa rádio, o entrevistado falou o que não devia
e não há prova: o sistema só guarda áudio contínuo por 60 minutos, e fora disso
só ficam os clipes de comerciais detectados.

A função é literalmente esta: escolher emissora, dia, início e fim; o sistema
grava esse trecho do stream e guarda por 15 dias. Ela protege as próximas
entrevistas. A que já foi ao ar não é recuperável.

### Relação com o plano

Não fere o §1.3: o item 2 exclui *reconhecer* conteúdo não cadastrado, e aqui
nada é reconhecido, só gravado. O pedido explícito do Dereck é a aprovação de
escopo exigida pela regra 3 do `CLAUDE.md`. Nenhuma seção do plano muda; a
documentação vai em `docs/features/`.

## 2. Decisões

| Decisão | Escolha | Motivo |
|---|---|---|
| Quem usa | Só `admin` | Sem vínculo com cliente. Se precisar mostrar a alguém, baixa o arquivo |
| Origem do áudio | Conexão própria com o stream | Vale para qualquer emissora, com ou sem campanha; caminho único; isolado da detecção |
| Aviso de falha | Tela + e-mail na hora para quem programou | A entrevista é ao vivo: avisar durante dá chance de agir |
| Duração | Máximo 6h, mesmo dia | Limite contra erro de digitação; quem cruza a meia-noite faz duas |
| Retenção | 15 dias contados do fim da gravação, sem prorrogação | Pedido do Dereck |
| Deploy com gravação em andamento | `deploy.sh` aborta, com bypass | A gravação roda na API; deploy corta o áudio |

**Custo:** ~56 MB/h por gravação (formato fixo, AAC 128 kbps: ver §5.2). Gravação de 6h ≈ 340 MB, pouco perto dos ~3 GB/dia de
evidência.

**Risco aceito:** emissora com campanha ativa fica com 2 conexões do mesmo IP
durante a gravação. Alguns painéis já baniram o IP da VM (2026-06), mas aqui são
poucas conexões extras, de curta duração.

## 3. Fora do escopo

- Repetição (semanal etc.), vínculo com cliente ou campanha, tools no MCP.
- Prorrogar os 15 dias.
- Acesso de cliente ou de `operator`.
- Métrica Prometheus e dashboard: logs bastam para o volume esperado.
- A gravação **não** altera `monitoring_status`, não entra em uptime nem em
  falhas de emissora, não aciona o matcher e não gera detecção.

## 4. Modelo de dados

Migration nova, no próximo número livre. Hoje seria 0071, mas `feat/mcp-server`
também disputa essa faixa: conferir no merge.

```sql
CREATE TABLE recordings (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  station_id      UUID REFERENCES stations(id) ON DELETE SET NULL,
  station_name    TEXT NOT NULL,        -- foto do nome: a linha sobrevive à emissora
  created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
  starts_at       TIMESTAMPTZ NOT NULL,
  ends_at         TIMESTAMPTZ NOT NULL,
  status          TEXT NOT NULL DEFAULT 'programada'
                  CHECK (status IN ('programada','gravando','processando','concluida',
                                    'parcial','falhou','cancelada','expirada')),
  object_key      TEXT,                 -- NULL até subir; volta a NULL ao expirar
  size_bytes      BIGINT,
  covered_seconds INTEGER,
  gaps            JSONB NOT NULL DEFAULT '[]',  -- [{"from":"...","to":"..."}]
  last_error      TEXT,
  drop_alerted_at TIMESTAMPTZ,          -- e-mail de queda sai uma vez por gravação
  canceled_at     TIMESTAMPTZ,          -- preenchido = termina `cancelada`
  finished_at     TIMESTAMPTZ,
  expires_at      TIMESTAMPTZ,          -- finished_at + 15 dias
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at),
  CHECK (ends_at - starts_at <= INTERVAL '6 hours')
);

CREATE INDEX recordings_pending_idx ON recordings (starts_at)
  WHERE status IN ('programada','gravando','processando');
CREATE INDEX recordings_expiry_idx ON recordings (expires_at)
  WHERE object_key IS NOT NULL;
```

Tabela nova, sem backfill e sem tocar em dado existente. `ON DELETE SET NULL`
porque `RESTRICT` travaria para sempre a exclusão de emissora ou usuário: as
linhas `expirada` ficam como histórico.

### Estados

| Status | Significado |
|---|---|
| `programada` | Aguardando o horário |
| `gravando` | Dentro da janela |
| `processando` | Janela acabou; juntando os pedaços e subindo o arquivo |
| `concluida` | Sem buraco de 30s ou mais |
| `parcial` | Com buraco de 30s ou mais; a tela lista os intervalos |
| `falhou` | Nenhum áudio capturado |
| `cancelada` | Cancelada; se já estava gravando, o que foi gravado fica salvo |
| `expirada` | Passou dos 15 dias; áudio apagado, linha mantida |

Transições: `programada → gravando → processando → concluida | parcial | falhou`.
`cancelada` vem de `programada` (sem áudio) ou de `gravando` (passa por
`processando` e termina `cancelada`, com áudio). `expirada` vem de qualquer
estado final com áudio. Cancelar não muda a regra da retenção: `expires_at` conta
de `finished_at`.

A coluna `gaps` é necessária porque **buracos não são preenchidos com silêncio**:
ao juntar os pedaços, o tempo do player deixa de corresponder ao relógio depois
de cada buraco. Como o arquivo é prova, a tela lista os intervalos que faltam
("sem áudio das 20:14:05 às 20:17:40").

## 5. Backend

Pacote novo `workers/internal/recording`, sem dependência de `supervisor`,
`ingestor.Worker` ou `index`.

### 5.1 Agendador

Goroutine única, iniciada em `cmd/api/main.go` **antes** da carga do índice, para
que o buraco após um reinício não inclua os minutos de boot. A cada 15s:

1. `programada` com `starts_at <= now() + 20s` e `ends_at > now()` → passa a
   `gravando` e dispara o gravador. O pré-roll compensa o tick e a conexão do ffmpeg:
   sem ele, gravação marcada para 20:00 só teria áudio a partir de ~20:00:20.
2. `gravando` sem gravador vivo no processo (reinício da API):
   - dentro da janela → retoma;
   - janela acabou → `processando`.
3. `processando` → finaliza (§5.3). Falhou, tenta de novo a cada 2 minutos.

`programada` com `ends_at` já vencido (API fora do ar durante toda a janela) vira
`falhou` com e-mail. Não há gravador vivo para ela e nunca haverá áudio.

Erro de banco numa etapa fica no log e não impede as seguintes: banco instável não pode
segurar a finalização de quem já gravou. As linhas que `FailMissed` devolve junto com
um erro são avisadas mesmo assim, porque o próximo tick não as encontra mais. Por isso
`FailMissed` é um comando só: fecha a linha e devolve os dados do e-mail juntos. Cada
etapa tem prazo próprio de 10s, para banco pendurado não parar o agendador. O prazo
vale só para as consultas: gravador e finalização nascem do contexto do processo.

O gravador relê o status antes de capturar. A linha que o tick tem na mão pode estar
velha: se o admin cancela depois da listagem, não há gravador para parar, e religar
com a linha velha gravaria depois do cancelamento e seguraria a finalização até
`ends_at`. Sem `gravando`, sai sem capturar. Se a leitura falhar ou passar de 10s,
também sai, e o próximo tick tenta de novo. `Cancel` muda o status antes de parar o gravador, pelo
mesmo motivo.

O Postgres é a fonte da verdade. A API tem uma instância só, então o mapa de
gravadores vivos em memória basta.

### 5.2 Gravador

Loop até `ends_at`, com `dir = /data/segments/_recordings/<id>/`:

```
ffmpeg -user_agent "VLC/3.0.20 LibVLC/3.0.20" <reconnect args> -i <stream_url> \
  -map 0:a:0 -c:a aac -b:a 128k -ar 44100 -ac 2 \
  -f segment -segment_time 60 -segment_format adts \
  -segment_atclocktime 1 -reset_timestamps 1 -strftime 1 \
  <dir>/a<unix-da-tentativa>-%Y%m%d-%H%M%S.adts
```

- **Reaproveita** de `ingestor` só o `pickReconnectArgs` (exportado como
  `ReconnectArgs`). Corrige de graça o caso de HLS que custou o incidente de
  2026-05-15.
- **Formato de áudio fixo, sempre reencodado (nunca `copy`).** Pedaços com
  parâmetros diferentes (ex.: 22 kHz mono + 48 kHz estéreo) concatenados geram um
  `.m4a` corrompido sem nenhum erro: 7s viraram 11,84s num experimento. Acontece
  quando a URL é trocada durante uma queda ou a rádio reinicia o encoder. O formato
  fixo elimina o caso e dispensa o `ProbeAudioCodec`, que abria uma conexão extra
  com o stream a cada tentativa.
- **`stream_url` é relida do banco a cada tentativa.** É o que dá sentido ao
  e-mail: trocar a URL durante a queda faz efeito na reconexão seguinte.
- **Stall watchdog:** o total de bytes em `dir` é medido a cada 10s. Sem crescimento
  por 60s, sai o alerta. Mede bytes, não arquivos, porque o pedaço abre no primeiro
  pacote e cresce durante o minuto. O ffmpeg pode ficar pendurado sem sair (mesma
  classe do incidente 2026-05-15).
- **Idade da tentativa:** alerta e morte são regras separadas. O relógio de bytes não
  zera entre tentativas, senão, com o stream fora, o alerta nunca sairia. Por isso ele
  não basta para matar: quando o stream volta, ele já está vencido, e um ffmpeg
  saudável leva ~16s até a primeira escrita. Morrer exige 60s sem bytes **e** tentativa
  com pelo menos 60s de idade. Ao matar, grava em `last_error` "stream sem dados há
  60s", para um ffmpeg pendurado não terminar `falhou` sem motivo.
- **Saiu antes da hora:** espera 5s e tenta de novo, até `ends_at`.
- **Prefixo `a<unix>`:** um ffmpeg novo no mesmo minuto não sobrescreve o pedaço
  anterior, nem depois de um reinício da API.
- **Extensão `.adts`:** o `segments-cleanup` só apaga `*.aac`. Usar o volume que já
  existe evita mudar compose e override em prod. Ele ainda apaga **pastas vazias**,
  então `MkdirAll` roda a cada tentativa: com o stream fora, a pasta some.
- **Fim da janela:** SIGTERM, para o último pedaço fechar direito. Mesmo tratamento
  no shutdown da API.

### 5.3 Finalização

1. Ordena os pedaços por horário (`segments` usa `time.Local` para interpretar o
   nome do arquivo; seguir a mesma convenção).
2. Apara cada pedaço no último frame ADTS completo, concatena e remuxa: `ffmpeg -i
   <junto> -c copy -movflags +faststart <id>.m4a`. M4A abre em qualquer player e
   permite pular trechos. ADTS cru não dá duração nem busca confiáveis. O aparo existe
   porque um pedaço cortado no meio de um frame (kill -9, crash da API) faz o remux
   parar ali, com exit 0 e arquivo curto, e o áudio dos pedaços seguintes some. Sobra
   de corte é sempre menor que um frame; com mais de 8191 bytes não reconhecidos num
   pedaço, a finalização para com erro em vez de aparar.
3. Calcula `gaps` pelo intervalo real de cada pedaço (início no nome, fim no mtime),
   ignorando trechos abaixo de 30s, e `covered_seconds` pela duração
   real do `.m4a` (`ffprobe`).
4. Confere o `.m4a` pela contagem de frames: tamanho maior que zero e duração no
   máximo 1 segundo abaixo de `frames × 1024 ÷ 44100`. A tolerância é absoluta porque
   a razão medida contra o ffmpeg real foi 1,000000: ela cobre só arredondamento, e
   um percentual deixaria passar minutos numa gravação longa. O exit 0 do remux não
   prova nada. O formato fixo dá 1024 amostras por frame, então a duração esperada é
   exata e independe do relógio; comparar com tempo de relógio daria alarme falso com
   rádio que engasga. Reprovado é falha de finalização, e os pedaços ficam.
5. Sobe para `recordings/<ano>/<mês>/<id>.m4a` no bucket de evidência.
6. Só depois de o upload confirmar: grava o status final e apaga os pedaços.

Sem nenhum frame completo depois da junção, só havia restos: vira `falhou` e não
sobe nada. Bytes não reconhecidos acima do tamanho de um frame nunca chegam aqui: se
o formato da captura mudar, áudio bom não pode ser descartado como "sem frames".
Qualquer falha da finalização grava o motivo em `last_error` e repete a cada 2
minutos: a linha fica em `processando` e os pedaços ficam no disco. A ordem garante
que nenhuma falha perca áudio já capturado.

Risco aceito: só corrupção menor que um frame. No máximo 8191 bytes por pedaço (cerca
de 0,5s) são descartados como sobra de corte, sem aparecer em `gaps`, que usa o mtime.
Acima disso a finalização para e preserva os pedaços.

### 5.4 E-mails

Via `mailer.Send` (noop sem SMTP), para o e-mail de `created_by`. Sem criador, a
informação fica só na tela.

| Quando | Assunto indica |
|---|---|
| Nenhum byte 60s após o início | Não conectou |
| Bytes pararam de crescer por 60s no meio da gravação | Captura parou, segue tentando. Não afirma que foi o stream: pode ser disco ou emissora excluída |
| Final `parcial` ou `falhou` | Resultado, com a cobertura |
| API fora durante toda a janela | Não gravou |
| 3ª falha seguida da finalização, uma vez por processo (a contagem fica em memória: reiniciar a API zera e pode repetir o e-mail) | Arquivo não fechou; áudio preservado, segue tentando |

Os dois primeiros casos são um alerta só por gravação, controlado por
`drop_alerted_at`. Em sucesso, nenhum e-mail.

O envio é assíncrono e não bloqueia a gravação: roda em goroutine própria, então e-mail
lento não atrasa o watchdog, o agendador nem a finalização. O `mailer` ignora contexto
(`smtp.SendMail` não recebe nenhum), então um SMTP mudo não tem prazo: só a goroutine
daquele envio fica presa até o processo reiniciar. O desligamento espera os envios em
andamento dentro do mesmo timeout dos gravadores, e aviso que surge depois disso é
descartado com log.

### 5.5 Retenção

Job de hora em hora, no padrão de `evidence/prune.go`: seleciona
`expires_at < now() AND object_key IS NOT NULL`, apaga o objeto e, **só se apagou**,
marca `expirada` e zera `object_key`. Ordem invertida deixaria objeto órfão que
nunca seria recuperado; falha de delete fica para a próxima passada.

Roda em goroutine própria, com ticker próprio, uma vez na subida e depois a cada hora:
um bucket lento não pode atrasar o início de uma gravação. Por ser um laço só, uma
passada nunca se sobrepõe à anterior.

### 5.6 API

Todas sob `RequireRole("admin")`:

| Método | Rota | Função |
|---|---|---|
| `POST` | `/v1/internal/recordings` | Criar |
| `GET` | `/v1/internal/recordings` | Listar, mais recentes primeiro, paginado |
| `POST` | `/v1/internal/recordings/{id}/cancel` | Cancelar `programada` ou `gravando` |
| `DELETE` | `/v1/internal/recordings/{id}` | Excluir: apaga objeto, depois linha. `409` se `gravando` ou `processando` |
| `GET` | `/v1/internal/recordings/{id}/audio` | Bytes pelo proxy da API. `404` sem áudio, inclusive com o objeto ausente no bucket |

O `DELETE` da linha é condicional (`status NOT IN ('gravando','processando')`), e não
só checado antes: o agendador pode reivindicar uma `programada` entre a leitura e a
exclusão, e apagar a linha tiraria a gravação da lista e da guarda de deploy com o
gravador rodando, deixando pasta órfã. Esse caso devolve o mesmo `409`.

Objeto ausente no bucket é `404`, não `500`: a linha pode ter expirado ou sido excluída
depois da leitura. Bucket fora do ar continua `500`.

Bytes pela API porque URL pré-assinada do MinIO não funciona no navegador em prod
(ver `docs/features/evidence-presigned-urls.md`). O handler usa
`context.WithoutCancel` na leitura do S3: o `middleware.Timeout` de 60s do router
cortaria um download grande no meio.

**Validação do `POST`** (dia e horas em `America/Sao_Paulo`):

- Emissora existe e tem `stream_url`.
- `ends_at > starts_at`, mesmo dia, de 1 minuto a 6h, com a duração medida depois de o início ser puxado para agora.
- `starts_at >= now() - 1min`, com a folga cobrindo o tempo de preencher o form.
  `starts_at` que passou por pouco grava a partir de agora, sem retroagir.
- Sem sobreposição com outra gravação viva da mesma emissora, para não abrir uma
  terceira conexão à toa.

### 5.7 `scripts/deploy.sh`

Antes do `up -d`, função `guard_active_recordings`:

```sql
SELECT count(*) FROM recordings
WHERE status = 'gravando'
   OR (status = 'programada' AND starts_at < now() + INTERVAL '10 minutes');
```

`processando` não bloqueia: a finalização é repetível, retoma sozinha depois do
reinício, e bloquear travaria todo deploy se uma finalização falhasse para sempre.

Acima de zero, aborta com a lista. Bypass: `SKIP_RECORDING_GUARD=yes`. Tabela
inexistente (primeiro deploy) conta como zero, para a guarda não travar o próprio
deploy que cria a tabela. Único caminho de deploy hoje: não há workflow em
`.github/workflows`.

## 6. Frontend

Rota `/admin/gravacoes`, página `AdminRecordingsPage.jsx`, item no `Sidebar`.

- **Formulário:** segue a barra `.flow-filters` e os componentes do design system.
  Seletor de emissora com a busca existente; aviso de que o horário é de Brasília.
  Ao salvar, roda o teste de stream da etapa Conexão: falha **avisa, não bloqueia**.
- **Lista:** emissora, dia e horário, status, cobertura, expira em, ações.
  Atualiza sozinha enquanto houver gravação viva.
- **Parcial:** selo com a cobertura e a lista dos intervalos sem áudio.
- **Ouvir:** `AudioPlayer` com blob via `fetch` autenticado, igual à evidência. O
  arquivo inteiro baixa antes de tocar; para gravações longas, o "Baixar" é o
  caminho indicado.
- **Baixar:** nome legível, `gravacao-<emissora>-<AAAA-MM-DD>-<HHhMM>.m4a`.

Sem dependência nova, então a regra 5 do `CLAUDE.md` não entra em jogo. Rota
autenticada, então `PUBLIC_ROUTES` não muda.

## 7. Falhas

| Situação | Resultado |
|---|---|
| Stream fora no início | Tenta até `ends_at`; e-mail aos 60s; se nunca voltar, `falhou` |
| Stream cai no meio | E-mail único, reconecta, buraco em `gaps`, termina `parcial` |
| ffmpeg pendurado sem sair | Watchdog mata em 60s e reinicia |
| URL trocada durante a queda | Vale na tentativa seguinte |
| Deploy com gravação viva | Bloqueado pela guarda |
| API reinicia mesmo assim | Retoma ao subir; buraco em `gaps`; pedaços anteriores preservados, com o frame cortado aparado na finalização; finalização interrompida retoma sem contar como falha |
| API fora durante toda a janela | `falhou` ao voltar, com e-mail |
| Bucket fora | Fica em `processando`, pedaços no disco, motivo em `last_error`, repete a cada 2 minutos; e-mail na 3ª falha seguida |
| Disco cheio | ffmpeg falha, buraco em `gaps`, e-mail de queda |
| Emissora ou usuário excluído depois | Linha mantida via `station_name` / `SET NULL` |

## 8. Testes

- **Unitários:** validação do `POST` (limites de 6h, mesmo dia, passado, sobreposição,
  fuso); transições de estado; cálculo de `gaps` e cobertura; ordem do prune (delete
  antes do marcado, falha mantém a linha); ordem da finalização (falha de upload não
  apaga pedaços).
- **Handlers:** `403` para `operator` e `viewer`; `409` no delete de gravação viva;
  `404` no áudio sem objeto.
- **Ponta a ponta local:** simulador Icecast (`docs/operations/simulacao-radio.md`,
  variante Icecast). Roteiro: gravação normal; stream derrubado no meio; API reiniciada
  no meio; stream que nunca sobe. Em cada um, abrir o `.m4a` e conferir duração,
  busca e `gaps`.
- **Migration:** contra cópia dos dados de prod (regra 4.8). Risco baixo por ser tabela
  nova, mas o `shadow_migration_test` roda de qualquer forma.
- **Antes do push:** `CGO_ENABLED=0 GOOS=linux go build ./...` (regra 6.1) e boot da API
  (regra 6.5).

## 9. Rollout

Full-stack, então dois pushes: backend primeiro com `deploy.sh`, smoke test na API,
depois frontend (o CF Pages publica sozinho a cada push em `master`).

Primeira gravação real: curta (5 minutos), numa emissora conhecida, ouvida até o fim
antes de confiar numa entrevista paga. Mesma lição da regra 4.5.

## 10. Documentação

- `docs/features/scheduled-recording.md`, com header YAML.
- Linha nova no mapa de consulta do `CLAUDE.md`.
- Índice em `docs/README.md`.
- Nota em `docs/operations/deploy.md` sobre a guarda e o bypass.
