FROM golang:1.23-alpine AS builder
LABEL org.opencontainers.image.source=https://github.com/mac-chaffee/ip-pass

WORKDIR /app
COPY go.* ./

RUN go mod download
COPY pkg/ ./pkg/
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags='-w -s -extldflags "-static"' -o /app/ip-pass ./pkg
RUN adduser -D -g '' appuser


FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /app/ip-pass /ip-pass

USER appuser

ENTRYPOINT ["/ip-pass"]
