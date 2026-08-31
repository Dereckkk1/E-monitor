---
status: implementado
ultima-verificacao: 2026-08-31
codigo-relacionado:
  - workers/internal/api/handlers/materials.go
  - frontend/src/utils/uploadOutcome.js
  - frontend/src/pages/CampaignWizardSteps/MaterialsStep.jsx
  - frontend/src/api/hooks.js
---

# Reuso de material no upload (dedup por `master_sha256`)

Quando o operador sobe um áudio que o cliente **já tem** cadastrado, o sistema
não cria um segundo material: reusa o existente. Este documento descreve como
esse reuso é sinalizado e por que ele não pode ser silencioso.

## Por que o dedup existe

Auditoria do caso UNIUBE 130/138: o mesmo áudio subido como **dois** materiais
fazia cada veiculação contar duas vezes — cada linha disparava o fan-out F-119
para as duas campanhas. `MaterialsHandler.Upload` passou a consultar
`GetByClientAndSHA(client_id, sha)` antes de inserir; havendo material com
aquele `master_sha256`, devolve o existente.

O escopo é **por cliente** de propósito: o mesmo áudio em clientes diferentes
pode ser produção legítima.

## Os dois desfechos do upload

O caminho é distinguido **pelo status HTTP**, e a distinção é informação de
negócio — não detalhe de transporte:

| Status | Significado | `fingerprint.generate` publicado? |
|--------|-------------|-----------------------------------|
| `201`  | Material novo criado | **Sim** — serviço Python gera o fingerprint e, no sucesso, publica `material.similarity-check` |
| `200`  | Dedup: devolveu material existente | **Não** — o handler retorna antes do publish |

Consequência do `200`, e a raiz de um diagnóstico caro em 2026-08-31: **não há
fingerprint pra gerar nem similaridade pra checar**. Como só o serviço Python
publica `material.similarity-check` (e só no caminho de sucesso), subir o mesmo
áudio duas vezes nunca dispara aviso de similaridade. Os dois sintomas que o
operador reporta — "não gera fingerprint" e "não avisa duplicata" — são o mesmo
mecanismo, e nenhum deles é defeito do pipeline.

**Diagnóstico de 10 segundos, sem acesso à VM:** DevTools → Network →
`POST /materials`. `201` = material novo. `200` = reuso. Cuidado: o
`POST /campaigns/{id}/materials` (o vínculo) aparece no DevTools também como
"materials" e responde `201` sem corpo — não confundir com o upload.

## Como a tela sinaliza

`planUploadOutcome` ([frontend/src/utils/uploadOutcome.js](../../frontend/src/utils/uploadOutcome.js))
recebe o status, o material e os ids já vinculados, e decide o desfecho. No
reuso, a fila de upload entra no estágio terminal `reused` em vez de encenar as
cinco etapas (`Enviando arquivo → Gerando fingerprint → …`), e o card diz o que
de fato aconteceu, nomeando o material que já existia.

`reused` é terminal mas **deliberadamente não conta como `done`**: o drawer só
fecha sozinho quando tudo terminou limpo, e aqui o operador precisa ler o aviso
antes de sair da tela.

## O reuso não relinka quando já está na campanha

`CampaignMaterials.Link` é um upsert:

```sql
ON CONFLICT (campaign_id, material_id) DO UPDATE
   SET target_stations = EXCLUDED.target_stations
```

e o wizard manda **todas** as emissoras da campanha. Relinkar um material que já
está vinculado, portanto, **sobrescreve silenciosamente o escopo de emissoras** —
um material restrito a uma emissora voltaria para todas as da campanha sem que
ninguém pedisse. Por isso `planUploadOutcome` devolve `shouldLink: false` quando
o material reusado já consta em `alreadyLinkedIds`. Re-upload é operação
inofensiva: não altera nada.

## Limpeza do arquivo órfão

O master é gravado em `<MASTERS_PATH>/<sha>.<ext>` **antes** da checagem de
dedup. No reuso esse arquivo não é referenciado por linha nenhuma do banco, e
cada retentativa deixava mais uma cópia de vários MB (visto em prod: o mesmo
áudio como `.mp3` referenciado e `.mpeg` órfão).

`discardRedundantMaster` remove a cópia redundante com duas guardas, ambas
contra perda irreversível:

1. **`newPath == existingPath`** → não remove. Re-upload do arquivo idêntico com
   a mesma extensão produz o mesmo `<sha>.<ext>`; remover apagaria o master de
   um material vivo e mataria a detecção daquele áudio para o cliente inteiro.
2. **Master do existente ausente do disco** → não remove. A cópia recém-enviada
   é a única que restou. Qualquer erro no `Stat` conta como ausente: na dúvida,
   não apaga.

Cobertura em `materials_test.go` (`TestDiscardRedundantMaster_*`), incluindo os
dois caminhos de perda de dado.

## O que o dedup NÃO pega

Só bytes idênticos. O mesmo áudio reencodado (outro bitrate, outro container)
gera `sha` diferente e passa como material novo — é aí que a checagem de
similaridade entra, e o modal bloqueante a partir de 0,50 de score. Materiais
221 e 220 do cliente UNIFIQUE são exatamente esse caso: mesmo spot, `sha`
diferente, `similarity_score = 1`, ambos vinculados à mesma campanha.

**Isso não gera dupla contagem.** Medido em 2026-08-31: os dois co-disparam no
mesmo segundo (24 pares, delta 0,0s, `confidence` e `temporal_coverage`
idênticos), mas as 24 detecções do 221 estão todas `retracted_at IS NOT NULL` —
a desambiguação pós-confirmação retrata um lado e o
[`ApprovedDetectionsFilter`](../../workers/internal/catalog/detection_filter.go)
descarta. Na base inteira, em 90 dias: 8.032 pares no mesmo segundo, dos quais
7.366 (91,7%) já retratados e 568 contando duas vezes (~0,7% do volume).

Ao investigar contagem de veiculação, **conte sempre com o
`ApprovedDetectionsFilter`**: `detection_campaigns.category` sozinho inclui
linhas retratadas e produz alarme falso. Ver
[detection-count-consistency.md](../architecture/detection-count-consistency.md)
e [version-disambiguation.md](../architecture/version-disambiguation.md).
