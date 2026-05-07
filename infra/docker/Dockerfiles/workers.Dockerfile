FROM golang:1.26-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY workers/go.mod workers/go.sum ./
RUN go mod download
COPY workers/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/diag ./cmd/diag

FROM alpine:3.19
RUN apk add --no-cache ffmpeg ca-certificates
COPY --from=builder /out/api  /usr/local/bin/api
COPY --from=builder /out/diag /usr/local/bin/diag
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api"]
