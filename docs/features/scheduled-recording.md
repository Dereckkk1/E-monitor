---
status: implementado
ultima-verificacao: 2026-11-09
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
  - frontend/src/utils/dial.js
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
| `concluida` | Com áudio, sem buraco de 30s ou mais e com menos de 30s de áudio faltando no total |
| `parcial` | Com áudio, e com um buraco de 30s ou mais ou 30s ou mais de áudio faltando no total; a tela lista os intervalos |
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
  uma nova tentativa começa depois de uma espera (`retryWait`). As
  `RetryFastTries` (5) primeiras falhas seguidas esperam `RetryDelay` (5s): com os
  ~7s de reconexão interna do ffmpeg, isso cobre cerca de um minuto de queda, o caso
  comum. Daí em diante a espera **dobra** até `RetryMax` (30s). Volta ao início
  quando uma tentativa grava bytes novos. Com retry fixo de 5s, um stream que recusa
  na hora levava ~720 conexões por hora do mesmo IP da detecção, padrão que já levou
  painel a banir a VM ([connect-backoff-circuit-breaker.md](connect-backoff-circuit-breaker.md));
  assim fica perto de 100. Dobrar desde a primeira falha foi testado e descartado:
  com ffmpeg real, uma queda de 40s virava buraco de 65s. O teto não é maior pelo
  mesmo motivo: cada segundo de espera depois de o stream voltar é áudio perdido de
  uma prova.
- `stream_url` é **relida a cada tentativa**: corrigir a URL durante uma queda vale
  na reconexão seguinte (os e-mails de queda dizem isso). A consulta tem o prazo do
  tick (`TickTimeout`, 10s). Se ela falhar (banco instável), a tentativa segue com a
  última URL conhecida e registra um aviso no log; sem URL conhecida, vira falha de
  captura. Emissora excluída (`ErrNotFound`) para de tentar mesmo com URL
  conhecida: `last_error = "a emissora foi excluída"`.
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
  buraco listado; abaixo disso, sozinho, é oscilação de rotação de arquivo. O início
  do pedaço seguinte é o momento em que o ffmpeg abriu o arquivo: o burst do servidor
  traz alguns segundos anteriores. Por isso a **duração** informada de um buraco pode
  sobrar um pouco, nunca faltar. A **posição** é aproximada: um buraco dentro de um
  pedaço (o servidor parou de mandar e voltou sem o ffmpeg reconectar) aparece no fim
  do pedaço, com erro de no máximo um pedaço (1 minuto).
- **Falta de áudio pelo total** (`AssessCoverage`). Olhar só a lista não basta: um
  stream oscilando 25s no ar e 25s fora perdeu 45% do áudio sem nenhum intervalo de
  30s, e saía `concluida`. Somar os intervalos entre pedaços também não serve: em HLS
  os frames chegam em rajadas, o pedaço de um minuto pode ter menos frames que o
  relógio e o seguinte mais, e a soma de falsos buracos de poucos segundos daria
  `parcial` em gravação perfeita. A decisão vem do total de áudio, que não sofre com a
  fronteira do pedaço:
  - `áudio da janela = duração do .m4a − pré-roll`, com `pré-roll = max(0, início
    da janela − início do primeiro pedaço)`;
  - `faltando = janela − áudio da janela`;
  - `não listado = faltando − soma dos buracos de 30s ou mais`.

  Se `não listado` chega a 30s (`MinGap`), a gravação é `parcial` e a lista é refeita
  com `DetailGap` = **5s**: aí já se sabe que falta áudio de verdade, e os intervalos
  curtos entram. Abaixo de 5s não lista, porque daria falso em HLS. A **tolerância é
  de 30s de áudio faltando não listado, no total**: um buraco único de 20s continua
  `concluida`, e dois de 20s viram `parcial` com os dois listados. É desenho, não
  furo: abaixo disso não dá para separar falta real de rajada.
- **`parcial` com lista vazia é possível:** muitos furos de menos de 5s somando 30s ou
  mais. A tela e o e-mail mostram o percentual, e o e-mail explica que faltam trechos
  curtos.
- **Status final** (`FinalStatus`): cancelada se houve cancelamento; `falhou` se não há
  áudio; `parcial` se `AssessCoverage` disse que falta áudio; senão `concluida`. O
  áudio serve de prova, então um buraco de 40s numa janela de 6h torna a gravação
  `parcial`, por menor que seja a fração. O percentual que a tela e o e-mail mostram é
  informação, não decide nada; em `parcial` ele para em 99%, porque a duração inclui o
  pré-roll e o burst e passaria de 100% mesmo com buraco.
- **Cancelada em andamento:** o fim da janela passa a ser o `canceled_at` quando ele
  vem antes do `ends_at`. Os buracos e o percentual são medidos até aí.
- **Buraco não vira silêncio.** Depois de um buraco, o tempo do player deixa de
  bater com o relógio. A prova do que foi ao ar em qual horário é a lista `gaps`, com
  as ressalvas acima: posição aproximada (até um pedaço) e falta abaixo da tolerância
  de 30s fora da lista.
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
| Fechou `parcial` | parcial (N%) | lista até 20 buracos com segundos e resume o resto ("e mais N trechos"); N para em 99 |
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
`scripts/deploy.sh` roda `guard_active_recordings` e aborta se existir, com `ends_at`
ainda no futuro:

- gravação `gravando`; ou
- gravação `programada` com `starts_at` em menos de 10 minutos.

Linha vencida (janela já passou, API fora do ar e ninguém fechou) não conta: ela não
pode barrar o hotfix que religaria a API.

A guarda roda **duas vezes**: antes do backup, como aviso cedo (deploy bloqueado não
gasta backup), e de novo **logo antes do `up -d`**. A segunda é a que protege: o build
leva minutos, uma gravação pode começar nesse meio-tempo, e até ali abortar não custa
nada, porque nenhum container foi recriado.

Ela lista as linhas (emissora, status, janela) e manda esperar ou cancelar em
`/admin/gravacoes`. Bypass: `SKIP_RECORDING_GUARD=yes ./scripts/deploy.sh`, que corta
o áudio enquanto a API reinicia. Depois de um reinício a gravação retoma sozinha. O
trecho perdido entra em `gaps` se tiver 30s ou mais; abaixo disso cai na tolerância
de 30s de áudio faltando não listado (ver Fechamento).

**`processando` não bloqueia.** A finalização é repetível e retoma sozinha depois do
reinício, sem perder áudio. Bloquear travaria todo deploy se uma finalização
falhasse para sempre.

A guarda é permissiva de propósito: tabela `recordings` inexistente conta como zero
(senão a guarda travaria o próprio deploy que cria a tabela), e `psql` inacessível ou
SQL com erro só avisa e segue. Guarda que dá alarme falso ensina a usar o bypass. A
consulta usa `psql -v ON_ERROR_STOP=1`: sem ele, SQL inválido sai com código 0 e saída
vazia, e a guarda liberaria calada em vez de avisar.

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
| `GET /recordings?limit&offset` | Lista, mais recentes primeiro. `limit` padrão 50, máximo 200 (acima disso, 200) |
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
- Toda linha traz `station_frequency_mhz`, `station_band`, `station_city`,
  `station_state` e `station_logo_url`, por `LEFT JOIN` na emissora atual, inclusive
  na consulta própria do `FailMissed`. Ficam nulos se a emissora foi excluída;
  `station_name` é o nome guardado na gravação e continua valendo.

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
- A lista de buracos de cada linha mostra no máximo 20 e resume o resto ("e mais N
  trechos"), como o e-mail. A cobertura de uma `parcial` para em 99%.
- O formulário avisa que o stream chega com atraso em relação ao ar e pede alguns
  minutos de folga antes e depois do horário: não existe pós-roll.
- **A emissora aparece como no seletor** (`StationIdentity`: logo, nome e
  "dial · cidade/UF", com `formatDial` de `utils/dial.js`, a mesma regra do
  `StationSearch`): num cartão logo abaixo da busca, porque o campo só mostra o nome e
  há emissoras com o mesmo nome em cidades diferentes; no recibo "Gravação programada"
  que aparece depois de programar, com o horário e o aviso de stream fora; e em cada
  linha da lista.
- Ações da lista são ícones no padrão de `/campaigns` (`btn btn-icon btn-sm`, com
  `title` e `aria-label`): ouvir/pausar, baixar, parar (gravando) ou cancelar
  (programada), excluir. Não existe editar: para mudar o horário, cancele e programe
  de novo.

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
pedaço: o `Concat` se recusa a aparar, e a linha não fecha sozinha.

### Lixo no pedaço: como fechar

Quem roda em prod é o Dereck. Os comandos usam o compose da VM com o override (regra
4.7 do `CLAUDE.md`); abaixo, `$C` é
`docker compose -f infra/docker/docker-compose.yml -f infra/docker/docker-compose.override.yml --env-file infra/docker/.env`.

1. **Ver o motivo.** Está na tela (linha `processando`) ou no banco:

   ```bash
   echo "SELECT id, station_name, last_error FROM recordings WHERE status = 'processando'" \
     | $C exec -T postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -At'
   ```

   O `last_error` traz o nome do pedaço: `recording: pedaço a<unix>-<AAAAMMDD-HHMMSS>.adts: N bytes não reconhecidos`.
2. **Achar os pedaços.** Ficam em `/data/segments/_recordings/<id>/` dentro do
   container `api` (em prod, `/mnt/data/segments/_recordings/<id>/` no host). Só
   entram na conta os arquivos com o nome `a<unix>-<AAAAMMDD-HHMMSS>.adts` e tamanho
   maior que zero.
3. **Tirar o pedaço ruim da pasta**, guardando uma cópia fora dela. Não renomeie dentro
   da pasta: depois do `Complete` a pasta inteira é apagada, e a cópia iria junto.

   ```bash
   sudo mkdir -p /mnt/data/recordings-quarentena/<id>
   sudo mv /mnt/data/segments/_recordings/<id>/<pedaço>.adts /mnt/data/recordings-quarentena/<id>/
   ```

   A próxima tentativa (até 2 minutos) junta o resto. O `Concat` para no **primeiro**
   pedaço ruim: se houver outro, o `last_error` passa a citá-lo, e o passo se repete.
   O minuto retirado vira buraco, e a gravação fecha `parcial`, com o trecho listado:
   é o retrato honesto do arquivo entregue. O áudio bom que estava no pedaço antes e
   depois do lixo fica na cópia, para recuperação manual se a prova precisar.
4. **Última saída, se nada fechar:** encerrar a linha sem áudio. Copie a pasta inteira
   para fora antes (ela vira órfã, ver Pasta órfã) e rode num psql
   (`$C exec postgres sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB"'`) com preview,
   primeiro com `ROLLBACK`; troque por `COMMIT` só depois de conferir que o `UPDATE`
   pegou 1 linha:

   ```sql
   BEGIN;
   SELECT id, station_name, status, last_error FROM recordings
    WHERE id = '<id>' AND status = 'processando';
   UPDATE recordings
      SET status = 'falhou', finished_at = now(), updated_at = now(),
          last_error = 'fechada à mão: ' || coalesce(last_error, '')
    WHERE id = '<id>' AND status = 'processando'
   RETURNING id, status;
   ROLLBACK; -- COMMIT depois de conferir
   ```

   O `status = 'processando'` no `WHERE` é o que torna isso seguro com a API de pé: se
   uma tentativa fechar a linha antes, o `UPDATE` não pega nada; se o `UPDATE` vencer,
   o `Complete` da tentativa em curso devolve `ErrBusy` e ela desiste sem repetir.
   Nenhum e-mail sai por esse caminho.

## Riscos aceitos

- **Segunda conexão com o stream.** Emissora com campanha ativa fica com 2 conexões
  do mesmo IP durante a gravação. Alguns painéis já baniram o IP da VM em 2026-06
  ([connect-backoff-circuit-breaker.md](connect-backoff-circuit-breaker.md)).
- **Até 1 frame corrompido por pedaço.** O aparo descarta o resto de frame no fim de
  cada pedaço cortado; é menos de 1 frame de AAC (~23 ms) por corte, e só ocorre em
  kill do ffmpeg ou crash da API.
- **SMTP mudo deixa uma goroutine presa até reiniciar a API** (ver E-mails).
- **Sobreposição não atômica** no agendamento (ver Regras da janela).
- **Tolerância de 30s de áudio faltando não listado** (ver Fechamento). Um buraco único
  de 20s sai `concluida`.
- **ffmpeg com HTTP chunked pode perder o rastreio depois de um stall.** Os bytes
  continuam crescendo, o watchdog não reage, e o áudio sai com falta: a gravação fecha
  `parcial`, não `concluida`.
- **Buraco dentro de um pedaço tem posição aproximada**, com erro de no máximo um
  pedaço (1 minuto). A duração é confiável; a posição, não exata.
- **"Gravar agora" espera até 15s** pelo próximo tick antes de conectar.
- **Silêncio digital pode gerar alerta falso de "captura parou".** Com silêncio o
  encoder AAC gera pouco byte, e o que ele gera só aparece em disco quando o bloco de
  ~256 KiB enche ou o pedaço fecha: o watchdog pode passar 60s sem ver crescimento.
- **Sem pós-roll.** A captura acaba em `ends_at`; como o stream atrasa em relação ao
  ar, a tela pede alguns minutos de folga antes e depois do horário.
- **Sem teto de gravações simultâneas.** Cada uma é um ffmpeg reencodando (CPU) e uma
  conexão a mais com o stream.

## Fora do escopo

Repetição, vínculo com cliente ou campanha, acesso de cliente ou `operator`,
prorrogar os 15 dias, tools no MCP. A gravação não altera `monitoring_status`, não
entra em falhas nem uptime e não gera detecção.
