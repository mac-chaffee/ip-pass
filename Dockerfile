FROM golang:1.23-alpine AS builder
LABEL org.opencontainers.image.source=https://github.com/mac-chaffee/ip-pass

RUN adduser -D -g '' appuser

FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY ./dist/pkg /ip-pass

USER appuser

ENTRYPOINT ["/ip-pass"]
