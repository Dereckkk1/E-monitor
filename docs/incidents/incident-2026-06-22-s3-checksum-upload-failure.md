---
status: implementado
ultima-verificacao: 2026-06-23
codigo-relacionado:
  - workers/internal/storage/s3.go
  - workers/internal/evidence/service.go
  - workers/internal/evidence/tiering.go
  - workers/go.mod
---

# Incidente 2026-06-22 — AWS SDK quebra todo upload de evidência (MinIO sem TLS)

## Sintoma (reportado pelo operador, 2026-06-23)

1. Na modal de `/detections`, a **maioria das veiculações** aparecia como
   **"falhou"** ao tentar ouvir a censura.
2. O sistema "voltou a dar 15s como 30s" (má-atribuição de duração reaparecendo).

## Linha do tempo

| Data | `available` | `failed` | Observação |
|------|-------------|----------|------------|
| 06-19 | 1062 | 0 | normal (imagem antiga) |
| 06-20 | 266 | 0 | normal |
| 06-21 | 213 | 0 | normal |
| **06-22** | 265 | **671** | precipício — uploads começam a falhar |
| **06-23** | **0** | 97 | ~100% falhando |

Disco **não** estava cheio (`/mnt/data` 65%, `/dev/root` 49%). A flag
`DISAMBIG_BY_COVERAGE` estava `true`, mas **não** era a causa.

## Causa raiz

O `aws-sdk-go-v2/service/s3` (v1.100.1 no `go.mod`) passou, numa mudança de
~jan/2025, a **calcular um checksum CRC em todo `PutObject` por padrão**
(`RequestChecksumCalculation = when_supported`). Contra um endpoint **sem TLS**
(o MinIO interno em `http://minio:9000`) com o body em **stream não-seekable**, o
SDK não consegue anexar o *trailing checksum* (que exige TLS) e o upload falha:

```
S3: PutObject, compute input header checksum failed,
unseekable stream is not supported without TLS and trailing checksum
```

O cliente S3 em `storage/s3.go` era montado sem nenhum override desse default.

**Por que disparou no dia 22 e não antes:** o `deploy.sh` faz `git pull` +
**rebuild** da imagem. O rebuild de 2026-06-22 recompilou com a versão nova do
SDK que já estava pinada no `go.mod` mas que a imagem de prod em execução
(buildada antes) ainda não tinha. Ou seja: **bug latente no `go.mod`, ativado
pelo primeiro rebuild.** Não tem relação com o conteúdo do deploy (padronização
de contagem / §18.2.2-v2b) — qualquer rebuild teria disparado.

### Cascata que ligou os dois sintomas

O fluxo de evidência é **extrai segmento → audit §9.9 → encode → upload**
([service.go](../../workers/internal/evidence/service.go)). Quando o upload (ou o
tiering hot→cold) falha, a detecção vira `evidence_status='failed'`:

- **"falhou" na modal** — `failed` não tem clipe pra tocar.
- **"15 como 30"** — quando a extração/upload morre, o **audit nem roda**; sem o
  audit, o 30s mal-atribuído **não é rejeitado** (`audit_rejected`) e fica
  contando como 30s. Ou seja, a má-atribuição não "voltou" no matcher — ela
  parou de ser **pega** porque o audit foi pulado.

## Correção

`fix(storage)` (commit `4871679`): força
`RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired` nos
clients S3 (upload + presign) em `storage/s3.go`, restaurando o comportamento
pré-mudança (sem CRC default). Sem isso, **qualquer rebuild futuro quebra de
novo**.

```go
cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String(endpoint)
    o.UsePathStyle = true
    o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
})
```

Mitigação equivalente sem rebuild (não usada, registrada para referência): env
`AWS_REQUEST_CHECKSUM_CALCULATION=when_required` no container da api (a SDK lê do
ambiente via `LoadDefaultConfig`).

## Ações pós-incidente

- [x] Fix em código (commit `4871679`) — não depende de env, sobrevive a rebuild.
- [ ] **Deploy** (`./scripts/deploy.sh`) — rebuilda com o fix; uploads voltam.
- [ ] **Verificar recuperação** — `evidence_status` por hora: `available` volta a
      subir, `failed` cai a ~0.
- [ ] **Clipes perdidos (22-23/06)** — as detecções `failed` não subiram o áudio.
      Se os segmentos ADTS ainda estiverem na retenção, dá pra regerar a evidência
      delas (follow-up; depende da janela de retenção dos segmentos).
- [ ] **Reconciliar a má-atribuição do período** — com o audit pulado, o 30s não
      foi rejeitado nem o 15s recuperado nessas datas; reavaliar após o upload
      normalizar (ver [version-disambiguation.md §18.2.2-v2b](../architecture/version-disambiguation.md)).

## Lição

Bump de dependência com **mudança de default de comportamento** (não de API) é
invisível no `go build` e só morde no **primeiro rebuild da imagem** — que pode
ser dias/semanas depois do bump, num deploy totalmente não relacionado. Endpoints
S3 **sem TLS** (MinIO local) são especialmente sensíveis a defaults novos do SDK.
Ao bumpar `aws-sdk-go-v2`, testar um `PutObject` real contra o MinIO http antes
de confiar no deploy.
