FROM golang:1.27-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/echo-service ./cmd/echo-service

FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=build /out/echo-service /app/echo-service

ENV ECHO_ADDR=0.0.0.0:8090 \
    ECHO_DATA_DIR=/app/data

EXPOSE 8090
VOLUME ["/app/data"]

ENTRYPOINT ["/app/echo-service"]
