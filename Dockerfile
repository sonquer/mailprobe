FROM golang:1.26-alpine AS builder

WORKDIR /build

COPY src/go.mod ./
RUN go mod download

COPY src/cmd/ cmd/
COPY src/internal/ internal/

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X github.com/sonquer/mailprobe/internal/version.Version=${VERSION} -X github.com/sonquer/mailprobe/internal/version.Commit=${COMMIT} -X github.com/sonquer/mailprobe/internal/version.Date=${BUILD_DATE}" \
    -o mailprobe ./cmd/mailprobe

FROM alpine:3.21

RUN apk --no-cache add ca-certificates && \
    adduser -D -H -s /sbin/nologin mailprobe

USER mailprobe

COPY --from=builder /build/mailprobe /usr/local/bin/mailprobe

EXPOSE 8080

ENTRYPOINT ["mailprobe"]
