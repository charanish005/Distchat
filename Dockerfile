# =============================================================================
# distchat — Dockerfile
# Builds a reproducible environment with Go, protoc, and all gRPC plugins.
# =============================================================================

FROM golang:1.21-bullseye AS builder

# ── Install protoc and system dependencies ────────────────────────────────────
RUN apt-get update && apt-get install -y --no-install-recommends \
      protobuf-compiler \
    && rm -rf /var/lib/apt/lists/*

# ── Install Go gRPC code-generation plugins ───────────────────────────────────
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest \
 && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# ── Copy source ───────────────────────────────────────────────────────────────
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# ── Generate gRPC stubs ───────────────────────────────────────────────────────
RUN mkdir -p proto/chat && \
    protoc \
      --go_out=. \
      --go_opt=paths=source_relative \
      --go-grpc_out=. \
      --go-grpc_opt=paths=source_relative \
      proto/chat.proto

# ── Build all binaries ────────────────────────────────────────────────────────
RUN go build -o /bin/distchat-server  ./server   && \
    go build -o /bin/distchat-client  ./client   && \
    go build -o /bin/distchat-loadtest ./loadtest

# =============================================================================
# Final lightweight runtime image
# =============================================================================
FROM debian:bullseye-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bin/distchat-server   /usr/local/bin/distchat-server
COPY --from=builder /bin/distchat-client   /usr/local/bin/distchat-client
COPY --from=builder /bin/distchat-loadtest /usr/local/bin/distchat-loadtest

# Also keep full Go source + tools for go run ./... usage
COPY --from=builder /usr/local/go        /usr/local/go
COPY --from=builder /root/go             /root/go
COPY --from=builder /app                 /app

ENV PATH="/usr/local/go/bin:/root/go/bin:$PATH"
WORKDIR /app

EXPOSE 50051 50052

CMD ["distchat-server", "--role=primary", "--port=50051"]
