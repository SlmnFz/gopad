FROM golang:1.22 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY web ./web

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/gopad ./cmd/gopad
RUN mkdir -p /out/data

FROM scratch

COPY --from=builder /out/gopad /gopad
COPY --from=builder --chown=65532:65532 /out/data /data

ENV GOPAD_ADDR=:8080 \
    GOPAD_DB_PATH=/data/gopad.db

VOLUME ["/data"]
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/gopad"]
