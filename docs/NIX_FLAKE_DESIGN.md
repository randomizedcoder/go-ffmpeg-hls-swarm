# Nix Flake Design for go-ffmpeg-hls-swarm

> **Type**: Reference Documentation *(optional)*
> **Audience**: Nix users, contributors
> **Related**: [DESIGN.md](DESIGN.md)

This document describes the Nix flake for reproducible development environments and builds. **You don't need Nix to use go-ffmpeg-hls-swarm** — it's an optional enhancement for those who prefer Nix.

---

## Table of Contents

- [Overview](#overview)
- [1. Goals](#1-goals)
- [2. Non-Goals](#2-non-goals)
- [3. Flake Structure](#3-flake-structure)
- [4. Supported Systems](#4-supported-systems)
- [5. Development Shell](#5-development-shell)
- [6. Package Definition](#6-package-definition)
- [7. Apps](#7-apps)
- [8. Checks](#8-checks)
- [9. Flake Outputs Summary](#9-flake-outputs-summary)
- [10. Usage Examples](#10-usage-examples)
- [11. CI/CD Integration](#11-cicd-integration)
- [12. Implementation](#12-implementation)

---

## Overview

This document describes the Nix flake configuration for `go-ffmpeg-hls-swarm`. The flake provides:

1. **Development shell** — All tools needed to develop and test the project
2. **Package builds** — Reproducible builds of the Go binary
3. **Apps** — Convenient runnable commands
4. **Checks** — Automated validation for CI
5. **Cross-platform support** — Linux and macOS (x86_64 and aarch64)

---

## 1. Goals

- Provide reproducible development environment via `nix develop`
- Build the Go binary with `nix build`
- Use `ffmpeg-full` for maximum codec/protocol support
- Support multiple architectures and operating systems
- Follow Nix flake best practices and idioms
- Use `writeShellApplication` for app scripts
- Provide `nix flake check` targets for CI
- Keep the flake simple and maintainable

## 2. Non-Goals

- NixOS module for running as a service (future enhancement)
- Docker/OCI image builds (can be added later)
- Cross-compilation (native builds only)

---

## 3. Flake Structure

```
go-ffmpeg-hls-swarm/
├── flake.nix           # Main flake definition
├── flake.lock          # Locked dependencies (auto-generated)
├── cmd/
│   └── go-ffmpeg-hls-swarm/
│       └── main.go
├── internal/
│   └── ...
├── go.mod
├── go.sum
└── ...
```

### Flake Inputs

| Input | Source | Purpose |
|-------|--------|---------|
| `nixpkgs` | `nixos-unstable` | Latest stable Go and FFmpeg |
| `flake-utils` | Standard | Multi-system helpers (`eachDefaultSystem`) |

### Why These Choices

- **nixos-unstable**: Latest Go (1.22+) and FFmpeg versions
- **flake-utils**: Idiomatic multi-platform support with `eachDefaultSystem`
- **Minimal inputs**: Keep dependency tree simple

---

## 4. Supported Systems

Using `flake-utils.lib.eachDefaultSystem`:

| System | Architecture | Notes |
|--------|--------------|-------|
| `x86_64-linux` | Linux (Intel/AMD) | Primary development target |
| `aarch64-linux` | Linux (ARM64) | Raspberry Pi, AWS Graviton |
| `x86_64-darwin` | macOS (Intel) | Developer laptops |
| `aarch64-darwin` | macOS (Apple Silicon) | M1/M2/M3 Macs |

### Platform-Specific Handling

```nix
# Using lib.optionals for conditional packages
packages = with pkgs; [
  go
  ffmpeg-full
] ++ lib.optionals stdenv.isLinux [
  # Linux-only packages
];

# Using lib.optionalAttrs for conditional env vars
env = {
  CGO_ENABLED = "0";
} // lib.optionalAttrs pkgs.stdenv.isDarwin {
  # macOS-specific env
};
```

---

## 5. Development Shell

### Included Tools

| Package | Purpose |
|---------|---------|
| `go` | Go compiler (latest from nixos-unstable) |
| `gopls` | Go language server |
| `gotools` | goimports, etc. |
| `golangci-lint` | Linting |
| `ffmpeg-full` | HLS testing (all codecs enabled) |
| `curl` | HTTP testing |
| `jq` | JSON processing |
| `nil` | Nix language server |

### Shell Definition

```nix
devShells.default = pkgs.mkShell {
  name = "go-ffmpeg-hls-swarm-dev";

  packages = with pkgs; [
    # Go toolchain
    go
    gopls
    gotools
    golangci-lint

    # Runtime dependency
    ffmpeg-full

    # Development utilities
    curl
    jq

    # Nix tooling
    nil
  ];

  # Environment variables (cleaner than shellHook exports)
  env = {
    CGO_ENABLED = "0";
    GOPATH = "$PWD/.go";
  };

  shellHook = ''
    export PATH="$PWD/.go/bin:$PATH"
    ${lib.getExe go-ffmpeg-hls-swarm-welcome}
  '';
};
```

---

## 6. Package Definition

### Build Strategy

Use `buildGoModule` for:
- Reproducible builds
- Automatic vendoring via `go.sum`
- Proper Go module handling

### Package Definition

```nix
packages = {
  go-ffmpeg-hls-swarm = pkgs.buildGoModule {
    pname = "go-ffmpeg-hls-swarm";
    inherit version;

    src = lib.cleanSourceWith {
      src = lib.cleanSource ./.;
      filter = path: type:
        let baseName = builtins.baseNameOf path;
        in !(builtins.elem baseName ignoredPaths);
    };

    vendorHash = null;  # Update after first build

    CGO_ENABLED = 0;

    subPackages = [ "cmd/go-ffmpeg-hls-swarm" ];

    ldflags = [
      "-s" "-w"
      "-X main.version=${version}"
    ];

    meta = with lib; {
      description = "HLS load testing tool using FFmpeg";
      homepage = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      license = licenses.mit;
      mainProgram = "go-ffmpeg-hls-swarm";
      platforms = platforms.unix;
    };
  };

  default = self.packages.${system}.go-ffmpeg-hls-swarm;
};
```

### Source Filtering

Keep builds clean by filtering out development artifacts:

```nix
ignoredPaths = [
  ".direnv"
  "result"
  ".go"
  ".git"
  ".vscode"
  ".cursor"
];

src = lib.cleanSourceWith {
  src = lib.cleanSource ./.;
  filter = path: type:
    let baseName = builtins.baseNameOf path;
    in !(builtins.elem baseName ignoredPaths);
};
```

---

## 7. Apps

Using `pkgs.writeShellApplication` for type-safe, reproducible scripts.

### Welcome App

Displayed when entering dev shell or running `nix run`:

```nix
go-ffmpeg-hls-swarm-welcome = pkgs.writeShellApplication {
  name = "go-ffmpeg-hls-swarm-welcome";
  runtimeInputs = with pkgs; [ go ffmpeg-full ];
  text = ''
    echo ""
    echo "🎬 go-ffmpeg-hls-swarm development shell"
    echo ""
    echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo "Go:     $(go version | cut -d' ' -f3)"
    echo "FFmpeg: $(ffmpeg -version 2>/dev/null | head -1 | cut -d' ' -f3)"
    echo ""
    echo "📦 go build ./cmd/go-ffmpeg-hls-swarm  - Build binary"
    echo "🧪 go test ./...                        - Run tests"
    echo "🔍 golangci-lint run                    - Lint code"
    echo "📦 nix build                            - Nix build"
    echo ""
  '';
};
```

### Build App

```nix
build = {
  type = "app";
  program = lib.getExe (pkgs.writeShellApplication {
    name = "go-ffmpeg-hls-swarm-build";
    text = ''
      echo "Building go-ffmpeg-hls-swarm..."
      nix build --print-out-paths
    '';
  });
};
```

### Run App (Execute Built Binary)

```nix
run = {
  type = "app";
  program = lib.getExe (pkgs.writeShellApplication {
    name = "go-ffmpeg-hls-swarm-run";
    runtimeInputs = [ pkgs.ffmpeg-full ];
    text = ''
      exec ${lib.getExe self.packages.${system}.default} "$@"
    '';
  });
};
```

### Apps Summary

```nix
apps = {
  welcome = {
    type = "app";
    program = lib.getExe go-ffmpeg-hls-swarm-welcome;
  };

  build = { ... };

  run = { ... };

  default = self.apps.${system}.welcome;
};
```

---

## 8. Checks

Automated validation for `nix flake check`.

### Go Checks Helper

```nix
mkGoCheck = name: script: pkgs.stdenvNoCC.mkDerivation {
  name = "go-ffmpeg-hls-swarm-${name}";
  src = src;

  nativeBuildInputs = with pkgs; [ go golangci-lint ];

  buildPhase = ''
    runHook preBuild
    export HOME=$TMPDIR
    export GOPATH=$TMPDIR/go
    export GOCACHE=$TMPDIR/go-cache
    ${script}
    runHook postBuild
  '';

  installPhase = ''
    mkdir -p $out
    echo "${name} passed" > $out/result
  '';
};
```

### Check Definitions

```nix
checks = {
  # Go formatting check
  format = mkGoCheck "format" ''
    echo "Checking Go formatting..."
    unformatted=$(gofmt -l .)
    if [ -n "$unformatted" ]; then
      echo "Unformatted files:"
      echo "$unformatted"
      exit 1
    fi
  '';

  # Go vet
  vet = mkGoCheck "vet" ''
    echo "Running go vet..."
    go vet ./...
  '';

  # Linting
  lint = mkGoCheck "lint" ''
    echo "Running golangci-lint..."
    golangci-lint run ./...
  '';

  # Unit tests
  test = mkGoCheck "test" ''
    echo "Running tests..."
    go test -v ./...
  '';

  # Build check
  build = self.packages.${system}.default;

  # Nix formatting
  nix-format = pkgs.stdenvNoCC.mkDerivation {
    name = "go-ffmpeg-hls-swarm-nix-format";
    src = ./.;
    nativeBuildInputs = [ pkgs.nixfmt-tree ];
    buildPhase = ''
      nixfmt --check flake.nix
    '';
    installPhase = ''
      mkdir -p $out
      echo "Nix format check passed" > $out/result
    '';
  };
};
```

---

## 9. Flake Outputs Summary

| Output | Command | Description |
|--------|---------|-------------|
| `packages.default` | `nix build` | Build Go binary |
| `packages.go-ffmpeg-hls-swarm` | `nix build .#go-ffmpeg-hls-swarm` | Same as default |
| `devShells.default` | `nix develop` | Dev environment |
| `apps.default` | `nix run` | Show welcome |
| `apps.welcome` | `nix run .#welcome` | Show welcome |
| `apps.build` | `nix run .#build` | Build and show path |
| `apps.run` | `nix run .#run -- <args>` | Run binary with FFmpeg |
| `checks.*` | `nix flake check` | All checks |
| `formatter` | `nix fmt` | Format Nix files |
| `overlays.default` | - | For other flakes |

---

## 10. Usage Examples

### Development

```bash
# Enter development shell
nix develop

# Or with direnv
echo "use flake" > .envrc
direnv allow

# Inside shell
go build ./cmd/go-ffmpeg-hls-swarm
go test ./...
golangci-lint run
```

### Building

```bash
# Build the package
nix build

# Result is in ./result/bin/hlsswarm
./result/bin/go-ffmpeg-hls-swarm --help

# Build and show path
nix run .#build
```

### Running

```bash
# Run directly with FFmpeg available
nix run .#run -- -clients 10 https://example.com/stream.m3u8
```

### Checking

```bash
# Run all checks
nix flake check

# Show flake info
nix flake show

# Format Nix files
nix fmt
```

---

## 11. CI/CD Integration

CI/CD integration is **out of scope** for now. We can revisit this when we're ready to set up automated builds and testing.

### 11.1 Future: NixOS Integration Tests

Since go-ffmpeg-hls-swarm targets Linux for high concurrency, we can leverage NixOS's VM-based testing framework (`nixosTest`) to run full integration tests:

```nix
# tests/integration.nix (future)

{ pkgs, ... }:

pkgs.nixosTest {
  name = "go-ffmpeg-hls-swarm-integration";

  nodes = {
    # HLS Origin Server (Nginx with test stream)
    origin = { config, pkgs, ... }: {
      services.nginx = {
        enable = true;
        virtualHosts."hls.test" = {
          root = "/var/www/hls";
          locations."~ \\.m3u8$".extraConfig = ''
            add_header Cache-Control "no-cache";
          '';
        };
      };
      # Generate test HLS stream
      systemd.services.generate-hls = {
        script = ''
          ${pkgs.ffmpeg}/bin/ffmpeg -f lavfi -i testsrc=duration=60:size=1280x720:rate=30 \
            -c:v libx264 -hls_time 2 -hls_list_size 10 \
            /var/www/hls/test.m3u8
        '';
        wantedBy = [ "multi-user.target" ];
      };
    };

    # go-ffmpeg-hls-swarm Client
    client = { config, pkgs, ... }: {
      environment.systemPackages = [
        pkgs.hlsswarm
        pkgs.ffmpeg
        pkgs.curl
      ];
    };
  };

  testScript = ''
    # Wait for origin to be ready
    origin.wait_for_unit("nginx.service")
    origin.wait_for_unit("generate-hls.service")
    origin.wait_for_open_port(80)

    # Run go-ffmpeg-hls-swarm test
    client.succeed(
      "go-ffmpeg-hls-swarm -clients 5 -duration 30s http://origin/test.m3u8"
    )

    # Verify metrics endpoint responded
    client.succeed("curl -s localhost:9090/metrics | grep hlsswarm_clients_active")
  '';
}
```

**Benefits of NixOS integration tests:**
- Full VM isolation (no host contamination)
- Reproducible test environment
- Tests the entire stack: FFmpeg, networking, metrics
- Can simulate network failures, resource limits

**Run with:**
```bash
nix build .#checks.x86_64-linux.integration-test
```

This is an advanced feature for Phase 2/3 when the implementation is stable.

---

## 12. Implementation

### Complete flake.nix

```nix
# go-ffmpeg-hls-swarm Nix Flake
#
# Provides a reproducible development environment and build
#
# Usage:
#   nix develop          # Enter dev shell
#   nix build            # Build the binary
#   nix run              # Show welcome banner
#   nix run .#run -- ... # Run with args
#   nix flake check      # Run all checks
#   nix fmt              # Format nix files
#
{
  description = "HLS load testing tool using FFmpeg process swarm";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
        lib = pkgs.lib;

        version = "0.1.0";

        ignoredPaths = [
          ".direnv"
          "result"
          ".go"
          ".git"
          ".vscode"
          ".cursor"
        ];

        src = lib.cleanSourceWith {
          src = lib.cleanSource ./.;
          filter = path: type:
            let baseName = builtins.baseNameOf path;
            in !(builtins.elem baseName ignoredPaths);
        };

        # Welcome banner app
        go-ffmpeg-hls-swarm-welcome = pkgs.writeShellApplication {
          name = "go-ffmpeg-hls-swarm-welcome";
          runtimeInputs = with pkgs; [ go ffmpeg-full ];
          text = ''
            echo ""
            echo "🎬 go-ffmpeg-hls-swarm development shell"
            echo ""
            echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
            echo "Go:     $(go version | cut -d' ' -f3)"
            echo "FFmpeg: $(ffmpeg -version 2>/dev/null | head -1 | cut -d' ' -f3)"
            echo ""
            echo "📦 go build ./cmd/go-ffmpeg-hls-swarm  - Build binary"
            echo "🧪 go test ./...                        - Run tests"
            echo "🔍 golangci-lint run                    - Lint code"
            echo "📦 nix build                            - Nix build"
            echo ""
          '';
        };

        # Helper for Go checks
        mkGoCheck = name: script: pkgs.stdenvNoCC.mkDerivation {
          name = "go-ffmpeg-hls-swarm-${name}";
          inherit src;
          nativeBuildInputs = with pkgs; [ go golangci-lint ];
          buildPhase = ''
            runHook preBuild
            export HOME=$TMPDIR
            export GOPATH=$TMPDIR/go
            export GOCACHE=$TMPDIR/go-cache
            ${script}
            runHook postBuild
          '';
          installPhase = ''
            mkdir -p $out
            echo "${name} passed" > $out/result
          '';
        };

      in {
        formatter = pkgs.nixfmt-tree;

        devShells.default = pkgs.mkShell {
          name = "go-ffmpeg-hls-swarm-dev";

          packages = with pkgs; [
            # Go toolchain
            go
            gopls
            gotools
            golangci-lint

            # Runtime dependency
            ffmpeg-full

            # Development utilities
            curl
            jq

            # Nix tooling
            nil
          ];

          env = {
            CGO_ENABLED = "0";
          };

          shellHook = ''
            export GOPATH="$PWD/.go"
            export PATH="$PWD/.go/bin:$PATH"
            ${lib.getExe go-ffmpeg-hls-swarm-welcome}
          '';
        };

        packages = {
          go-ffmpeg-hls-swarm = pkgs.buildGoModule {
            pname = "go-ffmpeg-hls-swarm";
            inherit version src;

            vendorHash = null; # Update after first build

            CGO_ENABLED = 0;

            subPackages = [ "cmd/go-ffmpeg-hls-swarm" ];

            ldflags = [
              "-s" "-w"
              "-X main.version=${version}"
            ];

            meta = with lib; {
              description = "HLS load testing tool using FFmpeg";
              homepage = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
              license = licenses.mit;
              mainProgram = "go-ffmpeg-hls-swarm";
              platforms = platforms.unix;
            };
          };

          default = self.packages.${system}.go-ffmpeg-hls-swarm;
        };

        apps = {
          welcome = {
            type = "app";
            program = lib.getExe go-ffmpeg-hls-swarm-welcome;
          };

          build = {
            type = "app";
            program = lib.getExe (pkgs.writeShellApplication {
              name = "go-ffmpeg-hls-swarm-build";
              text = ''
                echo "Building go-ffmpeg-hls-swarm..."
                nix build --print-out-paths
              '';
            });
          };

          run = {
            type = "app";
            program = lib.getExe (pkgs.writeShellApplication {
              name = "go-ffmpeg-hls-swarm-run";
              runtimeInputs = [ pkgs.ffmpeg-full ];
              text = ''
                exec ${lib.getExe self.packages.${system}.default} "$@"
              '';
            });
          };

          default = self.apps.${system}.welcome;
        };

        checks = {
          format = mkGoCheck "format" ''
            echo "Checking Go formatting..."
            unformatted=$(gofmt -l .)
            if [ -n "$unformatted" ]; then
              echo "Unformatted files:"
              echo "$unformatted"
              exit 1
            fi
          '';

          vet = mkGoCheck "vet" ''
            echo "Running go vet..."
            go vet ./...
          '';

          lint = mkGoCheck "lint" ''
            echo "Running golangci-lint..."
            golangci-lint run ./...
          '';

          test = mkGoCheck "test" ''
            echo "Running tests..."
            go test -v ./...
          '';

          build = self.packages.${system}.default;

          nix-format = pkgs.stdenvNoCC.mkDerivation {
            name = "go-ffmpeg-hls-swarm-nix-format";
            src = ./.;
            nativeBuildInputs = [ pkgs.nixfmt-tree ];
            buildPhase = ''
              nixfmt --check flake.nix
            '';
            installPhase = ''
              mkdir -p $out
              echo "Nix format check passed" > $out/result
            '';
          };
        };
      }
    ) // {
      # Overlay for other flakes to consume
      overlays.default = final: prev: {
        go-ffmpeg-hls-swarm = self.packages.${final.system}.default;
      };
    };
}
```

### Post-Implementation Steps

1. **Create flake.nix** with the above content
2. **Initialize Go module** (if not done):
   ```bash
   go mod init github.com/randomizedcoder/go-ffmpeg-hls-swarm
   ```
3. **First build** to get vendorHash:
   ```bash
   nix build
   # Will fail with correct hash, update flake.nix
   ```
4. **Test development shell**:
   ```bash
   nix develop
   go version
   ffmpeg -version
   ```
5. **Run checks**:
   ```bash
   nix flake check
   ```
6. **Add to .gitignore**:
   ```
   .go/
   .direnv/
   result
   result-*
   ```

### Optional: .envrc for direnv

```bash
# .envrc
use flake
```

---

## Appendix: Key Nix Idioms Used

| Pattern | Purpose |
|---------|---------|
| `flake-utils.lib.eachDefaultSystem` | Multi-platform support |
| `pkgs.writeShellApplication` | Type-safe shell scripts with deps |
| `lib.getExe` | Get executable path from derivation |
| `lib.cleanSourceWith` | Filter source for reproducibility |
| `lib.optionals` | Conditional list items |
| `lib.optionalAttrs` | Conditional attribute sets |
| `pkgs.mkShell` with `env` | Clean environment variables |
| `pkgs.nixfmt-tree` | Modern Nix formatter |
| `// { overlays }` | Top-level outputs outside `eachDefaultSystem` |
