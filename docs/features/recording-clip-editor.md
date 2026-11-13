---
status: implementado
ultima-verificacao: 2026-11-13
codigo-relacionado:
  - workers/internal/recording/editor.go
  - workers/internal/recording/service.go
  - workers/internal/recording/manager.go
  - workers/internal/api/handlers/recordings.go
  - workers/internal/api/router.go
  - frontend/src/pages/RecordingEditorPage.jsx
  - frontend/src/components/RecordingWaveform.jsx
  - frontend/src/utils/recordingEditor.js
  - frontend/src/pages/AdminRecordingsPage.jsx
---

# Editor de trechos da gravação

O lápis na lista de `/admin/gravacoes` abre `/admin/gravacoes/:id/editar`. Lá o admin
marca um ou vários trechos sobre a forma de onda e exporta só o que interessa, em MP3:
um trecho por vez ou todos num `.zip`. A gravação original não muda.

Complementa [scheduled-recording.md](scheduled-recording.md). Só admin, como o resto da
feature.

## Por que o corte é no servidor

Uma gravação chega a 6h (~340 MB de `.m4a`). Decodificada no navegador para Web Audio,
seriam ~7 GB de PCM. Então:

- **o servidor** calcula a forma de onda reduzida e corta com ffmpeg;
- **o navegador** desenha a onda (canvas), toca o `.m4a` e manda os tempos.

Não entrou biblioteca nova no frontend (a onda é desenhada à mão), então o
`package-lock.json` não muda (regra 5 do `CLAUDE.md`).

## API

Todas atrás de `RequireRole("admin")`, no mesmo grupo das outras de `/recordings`.

| Rota | O que faz |
|---|---|
| `GET /recordings/{id}` | A gravação (mesmo JSON da lista) |
| `GET /recordings/{id}/peaks` | Forma de onda em binário: 1 byte (0–255, pico absoluto) a cada 50 ms. Header `X-Peaks-Per-Second: 20`. 1h = 72 KB; 6h = 432 KB |
| `GET /recordings/{id}/clip?from=&to=&fade=1&name=` | Um trecho em MP3 128k (44,1 kHz, estéreo). `from`/`to` em segundos **do arquivo** |
| `POST /recordings/{id}/clips` | `{name, fade, clips:[{name, from, to}]}` → `.zip` com um MP3 por trecho (até 50) |

- **Peaks** são gerados na primeira vez (decodifica o arquivo inteiro para mono 8 kHz;
  dezenas de segundos numa gravação de 6h) e guardados no bucket em
  `recordings/AAAA/MM/<id>.peaks`, ao lado do `.m4a`. Falhar ao guardar não impede de
  entregar: só gera de novo na próxima vez.
- **Cópia local.** O `.m4a` é baixado do bucket uma vez para
  `<SEGMENTS_PATH>/_recordings/_editor/<id>.m4a` e reaproveitado pelos cortes seguintes
  (quem edita exporta vários trechos em sequência). Cada uso renova o mtime; o prune de
  hora em hora apaga cópias sem uso há mais de 1h (`EditorCacheTTL`) e sobras `.part` de
  download interrompido. URL pré-assinada não serve aqui: o `presignS3` aponta para o
  endpoint público, que pode não ser alcançável de dentro do container.
- **Validação antes do primeiro byte.** `from ≥ 0`, `to ≤ duração + 1s`, trecho de pelo
  menos 0,5s, no máximo 50 trechos. Erro vira 400 com mensagem em português. Depois que
  o MP3/zip começou a sair não dá mais para mudar o status: falha no meio só vai para o
  log, e o download chega truncado.
- **Corte preciso.** `-ss` antes do `-i` busca rápido no `.m4a` (tem índice por causa do
  `+faststart`) e, como há reencode, o corte cai na amostra.
- **Fade** (opcional, ligado por padrão na tela): 0,3s de entrada e saída, nunca mais que
  1/4 do trecho, para o corte não estalar.
- **Prazo próprio.** Peaks e cortes usam `context.WithoutCancel` (o `middleware.Timeout`
  de 60s do router cortaria no meio) com teto de 10 min (peaks) e 30 min (cortes).
  Cliente que desiste derruba o ffmpeg pela escrita que falha.
- **No máximo 2 ffmpeg do editor ao mesmo tempo** (`editorSlots`). A API também grava
  (um ffmpeg reencodando por gravação), e o editor não pode tomar a CPU dela. Quem
  espera mais de 2 minutos por vaga recebe 503 "o editor está ocupado".
- **Nomes de arquivo** saem limpos de caracteres que quebram Windows/zip
  (`SafeFileName`), e nomes repetidos no zip ganham " (2)", " (3)"…
- **Excluir** a gravação e **expirar** (15 dias) apagam também o `.peaks` e a cópia local.

## Tela

- **Barra de cima:** tocar/pausar, hora do ar da agulha (`14:32:10 ≈`) com a posição no
  arquivo embaixo, "Ir para" (digita `14:30` ou `14h30` e a agulha vai para lá com zoom de
  2 minutos), zoom (− / + / Tudo) e o progresso do download do áudio.
- **Visão geral:** a gravação inteira, com a janela de zoom (clique ou arraste para
  mover), os trechos marcados e os buracos.
- **Onda com zoom:** clique posiciona a agulha; arrastar cria trecho; as alças nas pontas
  ajustam início e fim; roda do mouse dá zoom em volta do cursor, Shift + roda desloca.
- **Lista de trechos:** nome editável, início e fim em hora do ar com ajuste de ±0,1s
  (clicar na hora leva a agulha até lá), duração, ouvir só o trecho, baixar MP3, excluir.
  Embaixo, "Fade curto nas pontas" e "Baixar todos (.zip)".
- **Atalhos** (não valem enquanto se digita): espaço toca/pausa; `I` marca o início e `O`
  fecha o trecho (com um trecho selecionado, `I`/`O` ajustam as pontas dele; `O` sem `I`
  fecha um trecho dos últimos 30s); `←`/`→` andam 1s (Shift: 5s); `+`/`-` zoom; `0` ver
  tudo; `Del` apaga o trecho selecionado; `Esc` limpa a seleção.
- A onda chega antes do áudio (é pequena), então dá para marcar trechos enquanto o
  `.m4a` baixa. Tocar espera o download.
- Os trechos ficam no `localStorage` do navegador por gravação: fechar a aba não perde a
  marcação (é conveniência por pessoa, não estado compartilhado).

### Hora do ar é aproximada (≈)

O arquivo não tem silêncio no lugar dos buracos: depois de um buraco, a hora anda sem o
arquivo andar. A conversão posição ↔ hora (`clockAt`/`offsetAt`) soma os buracos
conhecidos e estima o pré-roll (não fica guardado) pela diferença entre a duração do
arquivo e a janela menos os buracos, entre 0 e 20s. Fontes de erro: a posição de um buraco
tem erro de até 1 minuto, falta abaixo de 30s pode não estar listada, e o stream atrasa em
relação ao ar. Por isso o "≈" e a dica no relógio.

- **Buraco** aparece como uma faixa hachurada vermelha **num ponto** da onda (o áudio
  emenda ali), com a duração que falta. Um trecho que cruza um buraco ganha o aviso
  "Contém um buraco de Ns: o áudio emenda ali, falta esse tempo do ar".
- **Nome automático** do trecho: `Emissora 14h32-14h47` (no mesmo minuto ganha os
  segundos: `14h08m00s-14h08m13s`). Acompanha as alças até a pessoa renomear.

## Riscos aceitos

- **Primeiro "abrir" de uma gravação longa é lento:** o servidor baixa o `.m4a` do bucket
  e decodifica tudo para gerar a onda. Das próximas vezes, a onda vem pronta do bucket.
- **Áudio inteiro no navegador para tocar** (como o "ouvir" da lista): o endpoint
  `/audio` não trata `Range`. Para 6h são ~340 MB de download.
- **Disco:** a cópia local ocupa até ~340 MB por gravação em edição em `/mnt/data`, por
  até 1–2h (TTL + intervalo do prune).
- **Cópia local apagada no meio de um corte** pelo prune é inofensiva no Linux (arquivo
  aberto sobrevive ao unlink), e o mtime renovado a cada uso afasta o caso.
