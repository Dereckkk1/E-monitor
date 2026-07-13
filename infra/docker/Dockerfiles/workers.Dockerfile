FROM golang:1.26-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY workers/go.mod workers/go.sum ./
RUN go mod download
COPY workers/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/diag ./cmd/diag
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-shared-hashes ./cmd/backfill-shared-hashes
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-material-durations ./cmd/backfill-material-durations
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-geocoding ./cmd/backfill-geocoding
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/selfmatch ./cmd/selfmatch
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/audit-extent ./cmd/audit-extent
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-unretract-displaced ./cmd/backfill-unretract-displaced
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-twin-discriminative ./cmd/backfill-twin-discriminative
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/redisambiguate-twins ./cmd/redisambiguate-twins
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/backfill-recategorize ./cmd/backfill-recategorize

FROM alpine:3.19
# tzdata: necessário para o evidence tiering job carregar America/Sao_Paulo
# via time.LoadLocation. Sem isso, cai pra UTC silenciosamente e a janela
# diária de movimentação hot→cold desalinha por 3h. Observado nos logs como
# `evidence tiering: TZ load failed, using UTC, error: unknown time zone
# America/Sao_Paulo` (incidente 2026-05-12, F-114).
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=builder /out/api                     /usr/local/bin/api
COPY --from=builder /out/diag                    /usr/local/bin/diag
COPY --from=builder /out/backfill-shared-hashes  /usr/local/bin/backfill-shared-hashes
COPY --from=builder /out/backfill-material-durations  /usr/local/bin/backfill-material-durations
COPY --from=builder /out/backfill-geocoding  /usr/local/bin/backfill-geocoding
COPY --from=builder /out/selfmatch  /usr/local/bin/selfmatch
COPY --from=builder /out/audit-extent  /usr/local/bin/audit-extent
COPY --from=builder /out/backfill-unretract-displaced  /usr/local/bin/backfill-unretract-displaced
COPY --from=builder /out/backfill-twin-discriminative  /usr/local/bin/backfill-twin-discriminative
COPY --from=builder /out/redisambiguate-twins  /usr/local/bin/redisambiguate-twins
COPY --from=builder /out/backfill-recategorize  /usr/local/bin/backfill-recategorize
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
