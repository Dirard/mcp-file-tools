FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X main.version=$VERSION" -o /mcp-file-tools ./cmd/mcp-file-tools

FROM scratch
COPY --from=builder /mcp-file-tools /mcp-file-tools
USER 65532:65532
ENTRYPOINT ["/mcp-file-tools"]
