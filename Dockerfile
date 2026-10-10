# syntax=docker/dockerfile:1
FROM ghcr.io/typst/typst:0.15.1 AS typst
FROM golang:1.26-bookworm AS build
ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${REVISION}" -o /out/searchan ./cmd/searchan

FROM build AS test
COPY --from=typst /bin/typst /usr/local/bin/typst
RUN apt-get update && apt-get install -y --no-install-recommends fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/*
ENV SEARCHAN_TEST_TYPST=/usr/local/bin/typst
RUN go test -race -cover -timeout 90s ./...

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates fonts-noto-cjk \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --uid 10001 --create-home searchan \
    && mkdir /data && chown searchan:searchan /data
COPY --from=typst /bin/typst /usr/local/bin/typst
COPY --from=build /out/searchan /usr/local/bin/searchan
USER searchan
WORKDIR /data
ENV SEARCHAN_DATA_DIR=/data SEARCHAN_LISTEN=0.0.0.0:8090 GOMEMLIMIT=96MiB
RUN typst --version && searchan demo -out /tmp/render-smoke \
    && rm -rf /tmp/render-smoke
EXPOSE 8090
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["searchan", "healthcheck"]
ENTRYPOINT ["searchan"]
CMD ["serve"]
