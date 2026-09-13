# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.24-bookworm AS m-ui-build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG MUI_VERSION=dev

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
      -buildvcs=false \
      -trimpath \
      -ldflags="-s -w -X main.version=${MUI_VERSION}" \
      -o /out/m-ui \
      .


FROM debian:bookworm-slim AS mihomo-download

ARG MIHOMO_VERSION=1.19.30
ARG MIHOMO_SHA256=db214c7a2517e63c150d123178d16d102e03a241ccdae4e5e07ffbe9cf56c6f9

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl gzip \
    && rm -rf /var/lib/apt/lists/*

RUN set -eux; \
    archive="mihomo-linux-amd64-compatible-v${MIHOMO_VERSION}.gz"; \
    curl --fail --location --retry 3 \
      "https://github.com/MetaCubeX/mihomo/releases/download/v${MIHOMO_VERSION}/${archive}" \
      --output "/tmp/${archive}"; \
    echo "${MIHOMO_SHA256}  /tmp/${archive}" | sha256sum --check --strict; \
    gzip --decompress "/tmp/${archive}"; \
    mv "/tmp/${archive%.gz}" /tmp/mihomo; \
    chmod 0755 /tmp/mihomo


FROM debian:bookworm-slim

ARG MUI_VERSION=dev

LABEL org.opencontainers.image.title="m-ui" \
      org.opencontainers.image.description="A server management panel powered by Mihomo" \
      org.opencontainers.image.source="https://github.com/RomanovCaesar/m-ui" \
      org.opencontainers.image.licenses="GPL-3.0-only" \
      org.opencontainers.image.version="${MUI_VERSION}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /opt/m-ui

RUN mkdir -p /opt/m-ui/bin /opt/m-ui/bootstrap /opt/m-ui/core /opt/m-ui/data

COPY --from=m-ui-build /out/m-ui /opt/m-ui/bin/m-ui
COPY --from=mihomo-download /tmp/mihomo /opt/m-ui/bootstrap/mihomo
COPY docker/entrypoint.sh /usr/local/bin/m-ui-entrypoint

RUN chmod 0755 \
      /opt/m-ui/bin/m-ui \
      /opt/m-ui/bootstrap/mihomo \
      /usr/local/bin/m-ui-entrypoint

ENV TZ=UTC \
    MUI_DATA_DIR=/opt/m-ui/data \
    MUI_CORE_PATH=/opt/m-ui/core/mihomo

VOLUME ["/opt/m-ui/data", "/opt/m-ui/core"]

EXPOSE 2053/tcp 12080/tcp 12080/udp 2096/tcp

STOPSIGNAL SIGTERM

ENTRYPOINT ["/usr/local/bin/m-ui-entrypoint"]
