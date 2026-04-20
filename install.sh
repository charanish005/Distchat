#!/usr/bin/env bash
# =============================================================================
# distchat — Automated Dependency Installer
# COEN 6731 · Winter 2026 · Charanish Miriyala
#
# Supports: Ubuntu/Debian, macOS (with Homebrew)
# Usage:  chmod +x install.sh && ./install.sh
# =============================================================================

set -e

CYAN='\033[0;36m'
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # no colour

info()    { echo -e "${CYAN}[distchat]${NC} $1"; }
success() { echo -e "${GREEN}[distchat]${NC} $1"; }
error()   { echo -e "${RED}[distchat] ERROR:${NC} $1"; exit 1; }

# ── Detect OS ─────────────────────────────────────────────────────────────────
OS="$(uname -s)"
info "Detected OS: $OS"

# ── 1. Install Go 1.21+ ───────────────────────────────────────────────────────
info "Checking Go installation..."

if command -v go &>/dev/null; then
  GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
  MAJOR=$(echo "$GO_VERSION" | cut -d. -f1)
  MINOR=$(echo "$GO_VERSION" | cut -d. -f2)
  if [ "$MAJOR" -ge 1 ] && [ "$MINOR" -ge 21 ]; then
    success "Go $GO_VERSION already installed — OK"
  else
    info "Go $GO_VERSION found but 1.21+ required — upgrading..."
    INSTALL_GO=true
  fi
else
  INSTALL_GO=true
fi

if [ "$INSTALL_GO" = true ]; then
  if [ "$OS" = "Darwin" ]; then
    if ! command -v brew &>/dev/null; then
      error "Homebrew not found. Install it first: https://brew.sh"
    fi
    info "Installing Go via Homebrew..."
    brew install go
  elif [ "$OS" = "Linux" ]; then
    info "Installing Go 1.21 on Linux..."
    GO_TAR="go1.21.13.linux-amd64.tar.gz"
    curl -LO "https://go.dev/dl/${GO_TAR}"
    sudo rm -rf /usr/local/go
    sudo tar -C /usr/local -xzf "${GO_TAR}"
    rm "${GO_TAR}"
    export PATH="/usr/local/go/bin:$PATH"
    # Persist to shell profile
    for PROFILE in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
      if [ -f "$PROFILE" ]; then
        grep -q '/usr/local/go/bin' "$PROFILE" || \
          echo 'export PATH="/usr/local/go/bin:$PATH"' >> "$PROFILE"
      fi
    done
  else
    error "Unsupported OS. Please install Go manually: https://go.dev/dl/"
  fi
fi

# ── 2. Install protoc ─────────────────────────────────────────────────────────
info "Checking protoc installation..."

if command -v protoc &>/dev/null; then
  success "protoc $(protoc --version) already installed — OK"
else
  info "Installing protoc..."
  if [ "$OS" = "Darwin" ]; then
    brew install protobuf
  elif [ "$OS" = "Linux" ]; then
    sudo apt-get update -qq
    sudo apt-get install -y protobuf-compiler
  else
    error "Please install protoc manually: https://github.com/protocolbuffers/protobuf/releases"
  fi
fi

# ── 3. Install Go gRPC plugins ────────────────────────────────────────────────
info "Installing protoc-gen-go and protoc-gen-go-grpc..."

go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Add GOPATH/bin to PATH for this session and profile
GOPATH_BIN="$(go env GOPATH)/bin"
export PATH="$PATH:$GOPATH_BIN"

for PROFILE in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
  if [ -f "$PROFILE" ]; then
    grep -q "$GOPATH_BIN" "$PROFILE" || \
      echo "export PATH=\"\$PATH:$GOPATH_BIN\"" >> "$PROFILE"
  fi
done

success "Go plugins installed"

# ── 4. Install Go module dependencies ─────────────────────────────────────────
info "Running go mod tidy..."
go mod tidy
success "Go dependencies installed"

# ── 5. Generate gRPC stubs ────────────────────────────────────────────────────
info "Generating gRPC stubs from proto/chat.proto..."

mkdir -p proto/chat

protoc \
  --go_out=. \
  --go_opt=paths=source_relative \
  --go-grpc_out=. \
  --go-grpc_opt=paths=source_relative \
  proto/chat.proto

success "Generated proto/chat/chat.pb.go and proto/chat/chat_grpc.pb.go"

# ── 6. Quick build check ──────────────────────────────────────────────────────
info "Building all packages to verify..."
go build ./server ./client ./loadtest
success "All packages built successfully"

# ── Done ──────────────────────────────────────────────────────────────────────
echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  distchat setup complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "  Start primary server:   go run ./server --role=primary --port=50051"
echo "  Start backup server:    go run ./server --role=backup  --port=50052 --peer=localhost:50051"
echo "  Connect a client:       go run ./client --user=alice --room=general"
echo "  Run load test:          go run ./loadtest --clients=10 --messages=20"
echo ""
