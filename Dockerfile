FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /kumolo ./cmd/kumolo
RUN mkdir -p /data && chown 65532:65532 /data

FROM scratch

COPY --from=builder /kumolo /kumolo
COPY --from=builder --chown=65532:65532 /data /data

EXPOSE 5566

USER 65532:65532

ENTRYPOINT ["/kumolo"]
