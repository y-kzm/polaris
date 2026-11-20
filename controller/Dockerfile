# The whole RA controller in one image: backend (API and web UI) and modules.
# Build context: this directory.   docker compose up -d
FROM golang:1.25-bookworm AS go
WORKDIR /src
# The backend uses SQLite through cgo; the modules are plain Go.
COPY backend/ backend/
RUN cd backend && CGO_ENABLED=1 go build -o /out/ra-controller ./cmd/server
COPY modules/ modules/
RUN for m in modules/*/; do (cd "$m" && go build -o "/out/$(basename "$m")" .) || exit 1; done

FROM node:22-bookworm-slim AS ui
WORKDIR /src
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ .
RUN npm run build

FROM debian:bookworm-slim
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
        bash python3 python3-yaml procps ca-certificates >/dev/null \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/polaris
COPY --from=go /out/ bin/
COPY --from=ui /src/dist ui/
COPY server.sh param.yaml ./
ENV BIN_DIR=/opt/polaris/bin STATIC_DIR=/opt/polaris/ui DATA_DIR=/data NO_COLOR=1
VOLUME /data
ENTRYPOINT ["./server.sh"]
