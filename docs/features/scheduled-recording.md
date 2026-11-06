---
status: implementado
ultima-verificacao: 2026-11-06
codigo-relacionado:
  - workers/internal/recording/types.go
  - workers/internal/recording/window.go
  - workers/internal/recording/manager.go
  - workers/internal/recording/recorder.go
  - workers/internal/recording/finalize.go
  - workers/internal/recording/pieces.go
  - workers/internal/recording/ffmpeg.go
  - workers/internal/recording/notify.go
  - workers/internal/recording/service.go
  - workers/internal/recording/store.go
  - workers/internal/api/handlers/recordings.go
  - workers/internal/api/router.go
  - workers/cmd/api/main.go
  - migrations/0071_recordings.up.sql
  - scripts/deploy.sh
  - infra/docker/docker-compose.yml
  - frontend/src/pages/AdminRecordingsPage.jsx
  - frontend/src/utils/recordings.js
  - frontend/src/components/Sidebar.jsx
---

# Programar gravação

Admin escolhe emissora, dia, início e fim em `/admin/gravacoes`. O sistema grava
esse trecho do stream e guarda o áudio por 15 dias. Vale para qualquer emissora
com `stream_url`, com ou sem campanha.

Existe porque o sistema só guarda áudio contínuo por 60 minutos: uma entrevista
paga que deu problema não tinha prova. **Não recupera nada do passado.**

Spec: [2026-11-05-scheduled-recording-design.md](../superpowers/specs/2026-11-05-scheduled-recording-design.md)
(registro histórico: onde divergir deste doc, vale este doc e o código).

O pacote `workers/internal/recording` é isolado da detecção. Não usa supervisor,
worker nem índice, e não altera `monitoring_status` da emissora. A captura roda
**dentro do processo da API**, num `Manager` iniciado em `cmd/api/main.go` antes da
carga do índice (que leva minutos): cada minuto de boot depois de um reinício seria
um minuto de buraco.

## Como funciona

1. **Agendamento.** A cada 15s (`TickEvery`) o `Manager` passa a `gravando` o que
   começa em até 20s (`PreRoll`). O pré-roll compensa o atraso do tick e a conexão
   do ffmpeg. Consequência: o arquivo pode ter até ~20s de áudio antes do
   `starts_at`. Os buracos são medidos a partir do `starts_at`.
2. **Captura.** Um ffmpeg dedicado por gravação escreve pedaços de 1 minuto
   (`-f segment -segment_time 60`, alinhados ao relógio) em
   `<SEGMENTS_PATH>/_recordings/<id>/` (em prod, `/data/segments/_recordings/<id>/`).
   O nome é `a<unix da tentativa>-<AAAAMMDD-HHMMSS>.adts`. O prefixo da tentativa
   impede que um ffmpeg novo, no mesmo minuto, sobrescreva o pedaço do anterior.
3. **Finalização.** No fim da janela a linha vai a `processando`. Os pedaços são
   aparados, juntados, remuxados para `.m4a`, conferidos e enviados para
   `recordings/<AAAA>/<MM>/<id>.m4a` no bucket de evidência (`Content-Type:
   audio/mp4`). O ano e o mês vêm do `starts_at` em UTC.
4. **Fechamento.** Os pedaços só são apagados depois de o `Complete` confirmar no
   banco. Falha em qualquer ponto antes disso mantém tudo no disco e repete.
5. **Retenção.** Um laço próprio, de hora em hora (`PruneEvery`), apaga o objeto de
   quem passou do `expires_at` (fim da gravação + 15 dias) e só então marca
   `expirada`. A linha fica como histórico.

| Status | Significado |
|---|---|
| `programada` | Aguardando o horário |
| `gravando` | Dentro da janela |
| `processando` | Juntando e subindo o arquivo |
| `concluida` | Com áudio e sem buraco de 30s ou mais |
| `parcial` | Com áudio e com ao menos um buraco de 30s ou mais; a tela lista os intervalos |
| `falhou` | Nenhum áudio, ou janela inteira perdida porque a API estava fora |
| `cancelada` | Cancelada pelo admin; o que já tinha sido gravado fica |
| `expirada` | Passou dos 15 dias; áudio apagado |

## Regras da janela (`ParseWindow`)

- Dia e horários em **São Paulo**, no mesmo dia. O fim tem que ser depois do início;
  não atravessa a meia-noite.
- **Máximo de 6h**, também travado por `CHECK` na migration 0071.
- Início até 1 minuto no passado é aceito: o form tem resolução de minuto, e quem
  quer gravar agora escolhe o minuto em curso. Nesse caso o início é **puxado para
  agora**, para a janela não nascer com um buraco que nunca existiu. Mais de 1
  minuto no passado é erro ("o início já passou").
- **Mínimo de 1 minuto, medido depois de o início ser puxado para agora.** Escolher
  o minuto em curso com fim no minuto seguinte dá menos de 1 minuto efetivo e é
  recusado.
- Emissora sem `stream_url` é recusada. Sobreposição para a mesma emissora entre
  `programada`/`gravando` devolve 409. A checagem não é atômica (READ COMMITTED):
  dois admins ao mesmo tempo podem passar, e o custo é só uma conexão a mais com o
  stream.

## Captura e formato

- **Reencode fixo, nunca `-c copy`:** `-c:a aac -b:a 128k -ar 44100 -ac 2`. Pedaços
  com parâmetros diferentes (22 kHz mono e 48 kHz estéreo, por exemplo)
  concatenados geram um `.m4a` corrompido **sem erro nenhum**: 7s viraram 11,84s
  num experimento. Acontece quando a URL é trocada durante uma queda ou a rádio
  reinicia o encoder. Formato fixo elimina o caso. Custo: ~56 MB por hora, ~340 MB
  no limite de 6h, e CPU de encode por gravação.
- A conexão usa `-timeout 10s`, os args de reconexão do `ingestor` e o mesmo
  user-agent da detecção.
- Se o ffmpeg sai antes do fim da janela, vira falha de captura (`last_error`) e
  uma nova tentativa começa depois de `RetryDelay` (5s). `stream_url` é **relida a
  cada tentativa**: corrigir a URL durante uma queda vale na reconexão seguinte (os
  e-mails de queda dizem isso).
- A extensão `.adts` é de propósito: o sidecar `segments-cleanup` do compose apaga
  `*.aac` com mais de 60min, e uma gravação dura até 6h. Esse mesmo sidecar apaga
  **pastas vazias** (com o stream fora, a pasta da gravação fica vazia e some). Por
  isso o gravador faz `MkdirAll` a cada tentativa.

## Watchdog

Roda por gravação, a cada `PollEvery` (10s), e **mede bytes em disco**, não o
processo: o ffmpeg pode ficar vivo, pendurado e sem escrever (mesma classe do
incidente 2026-05-15). O ffmpeg grava em blocos de ~256 KiB, um a cada ~16s a 128
kbps, então o `StallAfter` de 60s comporta o intervalo normal entre escritas.

Alerta e morte são **regras separadas**:

- **Alerta:** segue só o relógio de bytes, que não zera entre tentativas. Com o
  stream fora, cada ffmpeg desiste sozinho e uma regra presa à tentativa nunca
  dispararia. Passados 60s sem crescimento, `MarkDropAlerted` garante **um** e-mail
  por gravação (a marca `drop_alerted_at` fica no banco, então sobrevive a
  reinício). "Não conectou" se nenhum byte foi gravado; "captura parou" caso
  contrário.
- **Morte:** derruba a tentativa em curso, e só se ela tiver **idade de pelo menos
  `StallAfter`**. Quando o stream volta, um ffmpeg saudável leva ~16s até a primeira
  escrita, e matá-lo antes disso criaria um laço de mortes. Grava
  `last_error = "stream sem dados há 60s"`, porque `Capture` não devolve erro quando
  é cancelado e a linha ficaria sem motivo.

## Fechamento: pedaços, buracos e status

- **Cada pedaço é aparado no último frame ADTS completo** (`Concat`, `completeFrames`).
  Um pedaço cortado no meio de um frame (kill -9, crash da API) fazia o remux parar
  ali **com exit 0** e perder em silêncio todo o áudio dos pedaços seguintes. O
  resto aparado é sempre menor que um frame; mais de 8191 bytes depois do último
  frame completo é lixo no meio do pedaço (ou formato não reconhecido), e então o
  `Concat` devolve erro em vez de aparar, para os pedaços ficarem no disco.
- **O `.m4a` é conferido pela contagem de frames.** `Concat` conta os frames
  entregues ao remux; como o formato é fixo (1024 amostras por frame a 44100 Hz), a
  duração esperada é exata. Se o `ffprobe` medir menos que isso menos 1s, ou o
  arquivo tiver 0 byte, a finalização falha e repete. Comparar com o relógio daria
  alarme falso com rádio que engasga.
- **Buraco:** `ComputeGaps` olha o intervalo entre o fim de um pedaço e o início do
  próximo (nome). O fim é o áudio do pedaço (`TrimToAudio`: início + frames), com o
  mtime como teto: quando o stream cai, o ffmpeg espera a reconexão antes de fechar o
  arquivo e o mtime anda ~12s sem áudio. Intervalo de **30s ou mais** (`MinGap`) é
  buraco; abaixo disso é oscilação de rotação de arquivo. O início do pedaço seguinte
  é o momento em que o ffmpeg abriu o arquivo: o burst do servidor traz alguns segundos
  anteriores, então o buraco informado pode sobrar um pouco, nunca faltar.
- **Status final pela lista de buracos, não por percentual** (`FinalStatus`):
  cancelada se houve cancelamento; `falhou` se não há áudio; `parcial` se há
  qualquer buraco; senão `concluida`. O áudio serve de prova, então um buraco de
  40s numa janela de 6h torna a gravação `parcial`, por menor que seja a fração.
  O percentual que a tela e o e-mail mostram é informação, não decide nada.
- **Cancelada em andamento:** o fim da janela passa a ser o `canceled_at` quando ele
  vem antes do `ends_at`. Os buracos e o percentual são medidos até aí.
- **Buraco não vira silêncio.** Depois de um buraco, o tempo do player deixa de
  bater com o relógio. A prova do que foi ao ar em qual horário é a lista `gaps`.
- Sem nenhum frame aproveitável (nenhum pedaço, ou só restos de frame) a linha
  fecha sem áudio: `falhou`, ou `cancelada` se cancelada. Só `falhou` manda e-mail.

## Finalização repetível

A finalização é idempotente e **nunca perde áudio**: a chave do objeto é fixa
(`recordings/<AAAA>/<MM>/<id>.m4a`), e os pedaços só somem depois do `Complete`. O
único descarte deliberado são os restos de frame descritos acima.

- Qualquer falha (disco, ffmpeg, bucket, banco) grava o motivo em `last_error`, que
  a tela mostra, e a próxima tentativa só ocorre depois de **`FinalizeRetry` = 2
  minutos**. O espaçamento existe porque cada tentativa reescreve o arquivo inteiro
  em disco, até ~330 MB, e com o bucket fora o tick de 15s faria isso sem parar. O
  id fica em `finalizing` durante a espera, e é isso que espaça.
- **`FinalizeTimeout` = 30 minutos** por tentativa: folga para juntar e subir o
  maior arquivo. Sem prazo, um upload pendurado prenderia o id para sempre. Estourar
  o prazo conta como falha.
- Desligamento da API no meio de uma finalização **não é falha**: não grava
  `last_error`, não conta para o aviso, e a finalização retoma no boot.
- `Complete` só atua sobre `processando`. `ErrBusy` (outro caminho já fechou a linha)
  é tratado como sucesso: não há o que repetir.
- `Complete` com `concluida` limpa `last_error`; os demais status o mantêm.
  Portanto uma `parcial` ou `cancelada` pode mostrar um motivo antigo de captura.
- Enquanto o gravador ainda fecha o último pedaço (cancelamento), a finalização
  espera o próximo tick.

## E-mails

São avisos; o estado real está na tela. Vão só para o **criador** da gravação (sem
criador, usuário excluído, não sai nada). Assunto: `Gravação de <emissora>: <motivo>`.

| Quando | Assunto | Observação |
|---|---|---|
| 60s sem bytes e nenhum byte gravado | não conectou | uma vez por gravação |
| 60s sem bytes depois de já ter gravado | captura parou | uma vez por gravação, compartilha a marca com o anterior |
| Janela inteira passou com a API fora (`FailMissed`) | não gravou | a linha vai a `falhou` |
| Fechou sem nenhum áudio | falhou | traz o `last_error`, cortado em 300 caracteres |
| Fechou `parcial` | parcial (N%) | lista cada buraco com segundos |
| 3ª falha seguida de finalização | arquivo não fechou | o áudio está preservado |

Não há e-mail para `concluida` nem `cancelada`.

- **O contador do "arquivo não fechou" é por processo** (mapa em memória). Sai uma
  vez quando chega a 3 e não repete até a finalização ter sucesso. Reiniciar a API
  zera a contagem e o e-mail pode sair de novo. É de propósito: uma falha transitória
  de bucket não pode virar alarme.
- **Envio assíncrono, em goroutine própria.** O `mailer` usa `smtp.SendMail`, que
  **ignora o contexto**: o prazo de 30s do envio não tem efeito com SMTP mudo.
  Esperar ali prenderia o watchdog, o agendador e a finalização para sempre. Com
  SMTP mudo só a goroutine do e-mail fica presa, até o processo reiniciar. Risco
  aceito.
- Depois de `WaitRecorders` começar, e-mail novo é descartado com log.

## Deploy

A captura roda dentro da API, então recriar a API corta o áudio. O
`scripts/deploy.sh` roda `guard_active_recordings` **antes do `git pull`** e aborta
se existir:

- gravação `gravando`; ou
- gravação `programada` com `starts_at` em menos de 10 minutos.

Ele lista as linhas (emissora, status, janela) e manda esperar ou cancelar em
`/admin/gravacoes`. Bypass: `SKIP_RECORDING_GUARD=yes ./scripts/deploy.sh`, que corta
o áudio enquanto a API reinicia. Depois de um reinício a gravação retoma sozinha e o
trecho perdido entra em `gaps`.

**`processando` não bloqueia.** A finalização é repetível e retoma sozinha depois do
reinício, sem perder áudio. Bloquear travaria todo deploy se uma finalização
falhasse para sempre.

A guarda é permissiva de propósito: tabela `recordings` inexistente conta como zero
(senão a guarda travaria o próprio deploy que cria a tabela), e `psql` inacessível só
avisa e segue. Guarda que dá alarme falso ensina a usar o bypass.

### Desligamento

No `SIGTERM` o `main.go` espera os gravadores **em paralelo** ao `srv.Shutdown`
(`WaitRecorders(8s)`): em série os prazos somariam. O ffmpeg precisa do `WaitDelay`
de 5s depois do SIGTERM para fechar o pedaço (até ~16s de áudio em buffer). Os 8s
ficam acima disso e abaixo do **`stop_grace_period: 20s`** do serviço `api` no
compose: com o padrão de 10s, o `Shutdown` empata com o prazo e o SIGKILL corta o
ffmpeg. Gravações em curso ficam `gravando` e são retomadas no boot. Depois de
`WaitRecorders` começar, não nasce gravador novo.

## API

Tudo em `/recordings`, só `admin` (`RequireRole("admin")` no router; `operator`
recebe 403).

| Rota | O que faz |
|---|---|
| `GET /recordings?limit&offset` | Lista, mais recentes primeiro. `limit` padrão 50, máximo 200 |
| `POST /recordings` | Agenda: `station_id`, `day`, `start`, `end`. 400 de validação traz mensagem em português; 409 em sobreposição |
| `POST /recordings/{id}/cancel` | `programada` vira `cancelada`; `gravando` para o ffmpeg e vai a `processando`. Outro estado: 409 |
| `DELETE /recordings/{id}` | Apaga o objeto e depois a linha. `gravando` e `processando`: 409 (cancele antes). A condição também está no `DELETE` SQL, porque o tick pode reivindicar entre a leitura e a escrita |
| `GET /recordings/{id}/audio` | Entrega o `.m4a` |

- **O `/audio` usa `context.WithoutCancel`.** O `middleware.Timeout` de 60s do router
  cancelaria o contexto que governa a leitura do S3 no meio de um download de
  340 MB. A desistência do cliente continua detectada pelo erro de escrita do
  `io.Copy`.
- **Os bytes passam pela API**, não por URL pré-assinada: a do MinIO não funciona no
  navegador em prod ([evidence-presigned-urls.md](evidence-presigned-urls.md)). O
  endpoint não trata `Range`, então o front baixa o arquivo como blob para tocar ou
  salvar.
- Objeto ausente no bucket vira 404 ("sem áudio").

## Frontend

`/admin/gravacoes` (`AdminRecordingsPage.jsx`, formatação em `utils/recordings.js`).

- O **item de menu só aparece para `admin`** (`Sidebar.jsx`). A rota, porém, usa
  `RequireRole roles={['admin']}`, que trata `operator` como `admin`: **pela URL o
  `operator` chega à tela e recebe 403 da API**. A API é o controle real.
- Horários em São Paulo, independente do fuso do navegador.
- `errorMessage` mostra o texto da API só em 4xx de validação; o resto (HTML de
  502/504, erro de rede, texto técnico em inglês) vira mensagem genérica.
- A tela consulta de novo enquanto houver gravação `programada`, `gravando` ou
  `processando`.

## Pasta órfã

Um crash entre o fechamento do status e a remoção dos pedaços deixa
`_recordings/<id>/` no disco. Nada volta para limpar, porque a gravação já fechou. A
extensão `.adts` também tira a pasta do alcance do `segments-cleanup`.

Procedimento manual: consulte a linha pelo `<id>` da pasta. Se ela estiver em estado
final (`concluida`, `parcial`, `falhou`, `cancelada`, `expirada`) ou não existir, a
pasta pode ser apagada à mão. Com `programada`, `gravando` ou `processando`, não
apague: são os pedaços da gravação.

## Linha presa em `processando`

Os pedaços ficam no disco, nenhum áudio se perde e a finalização repete a cada 2
minutos. A tela mostra o motivo (`last_error`). O caminho é investigar o motivo,
corrigir a causa (bucket, disco, ffmpeg) e deixar a próxima tentativa fechar a linha.
O e-mail "arquivo não fechou" sai na 3ª falha seguida de cada processo. Não apague a
pasta: ela é a única cópia do áudio até o `Complete` confirmar.

Motivo `m4a incompleto: ... esperados ...` quer dizer que o remux ou o `ffprobe`
entregaram menos áudio que os frames juntados; o arquivo foi descartado e os pedaços
mantidos. Motivo `pedaço ...: N bytes não reconhecidos` quer dizer lixo no meio de um
pedaço: o `Concat` se recusa a aparar, e a causa precisa de olho humano antes de a
linha fechar.

## Riscos aceitos

- **Segunda conexão com o stream.** Emissora com campanha ativa fica com 2 conexões
  do mesmo IP durante a gravação. Alguns painéis já baniram o IP da VM em 2026-06
  ([connect-backoff-circuit-breaker.md](connect-backoff-circuit-breaker.md)).
- **Até 1 frame corrompido por pedaço.** O aparo descarta o resto de frame no fim de
  cada pedaço cortado; é menos de 1 frame de AAC (~23 ms) por corte, e só ocorre em
  kill do ffmpeg ou crash da API.
- **SMTP mudo deixa uma goroutine presa até reiniciar a API** (ver E-mails).
- **Sobreposição não atômica** no agendamento (ver Regras da janela).

## Fora do escopo

Repetição, vínculo com cliente ou campanha, acesso de cliente ou `operator`,
prorrogar os 15 dias, tools no MCP. A gravação não altera `monitoring_status`, não
entra em falhas nem uptime e não gera detecção.
