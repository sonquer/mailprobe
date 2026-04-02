FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY src/go.mod ./
RUN go mod download

COPY src/cmd/ cmd/
COPY src/internal/ internal/

ARG VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/sonquer/mailprobe/internal/version.Version=${VERSION}" \
    -o mailprobe ./cmd/mailprobe

FROM alpine:3.21

RUN apk --no-cache add ca-certificates && \
    adduser -D -H -s /sbin/nologin mailprobe

USER mailprobe

COPY --from=builder /build/mailprobe /usr/local/bin/mailprobe

EXPOSE 8080

ENTRYPOINT ["mailprobe"]
