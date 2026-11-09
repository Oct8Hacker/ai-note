FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates wget \
    && apt-get clean
COPY --from=build /out/gateway /app/gateway
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
