FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM debian:bookworm-slim
# yt-dlp must stay current: YouTube changes break old releases (2025.10.22 got
# 403 Forbidden on every stream URL by Nov 2026).
ARG YTDLP_VERSION=2026.10.26
# yt-dlp needs an external JavaScript runtime to solve YouTube's challenges;
# Deno (>= 2.3.0) is its recommended runtime and is used automatically when on PATH.
ARG DENO_VERSION=2.9.8
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates wget unzip \
    && rm -rf /var/lib/apt/lists/* \
    && cd /tmp \
    && wget -q "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp_linux" \
         "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/SHA2-256SUMS" \
    && grep ' yt-dlp_linux$' SHA2-256SUMS | sha256sum -c - \
    && install -m 0755 yt-dlp_linux /usr/local/bin/yt-dlp \
    && wget -q "https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-x86_64-unknown-linux-gnu.zip" \
         "https://github.com/denoland/deno/releases/download/v${DENO_VERSION}/deno-x86_64-unknown-linux-gnu.zip.sha256sum" \
    && sha256sum -c deno-x86_64-unknown-linux-gnu.zip.sha256sum \
    && unzip -q deno-x86_64-unknown-linux-gnu.zip -d /usr/local/bin \
    && chmod 0755 /usr/local/bin/deno \
    && rm -f /tmp/yt-dlp_linux /tmp/SHA2-256SUMS /tmp/deno-x86_64-unknown-linux-gnu.zip* \
    && yt-dlp --version && deno --version
COPY --from=build /out/worker /app/worker
ENTRYPOINT ["/app/worker"]
