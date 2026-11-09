FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM debian:bookworm-slim
ARG YTDLP_VERSION=2025.10.22
RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates wget \
    && apt-get clean \
    && wget -qO /usr/local/bin/yt-dlp "https://github.com/yt-dlp/yt-dlp/releases/download/${YTDLP_VERSION}/yt-dlp_linux" \
    && chmod +x /usr/local/bin/yt-dlp
COPY --from=build /out/worker /app/worker
ENTRYPOINT ["/app/worker"]
