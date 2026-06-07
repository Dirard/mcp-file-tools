FROM golang:1.26-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o server ./cmd/mcp-file-tools

FROM alpine:latest

WORKDIR /app
COPY --from=builder /app/server ./
ENV MCP_CWD_REQUIRE_EXPLICIT_STATE_PATH=true

EXPOSE 8787

ENTRYPOINT ["/app/server"]
CMD ["--http", "0.0.0.0:8787"]
