# Comprehensive Nix Builds Design

> **Type**: Design Document
> **Status**: Draft
> **Related**: [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md), [TEST_ORIGIN.md](TEST_ORIGIN.md), [CLIENT_DEPLOYMENT.md](CLIENT_DEPLOYMENT.md)

This document provides a holistic design for all Nix builds in the repository, covering:
1. **Cross-platform support** — Enable builds on macOS (Darwin) and other Nix-supported systems
2. **Development shell** — Ensure `nix develop` provides all necessary tools
3. **Main binary container** — OCI container for `go-ffmpeg-hls-swarm` binary
4. **Test origin deployment variants** — Runner, basic container, enhanced container, MicroVM, and ISO images
5. **DRY configuration** — Reusable NixOS modules shared across all deployment variants

---

## Table of Contents

- [Overview](#overview)
- [Goals](#goals)
- [Current State](#current-state)
- [Architecture Overview](#architecture-overview)
- [Design Decisions](#design-decisions)
- [Implementation Plan](#implementation-plan)
- [Configuration Reuse Strategy](#configuration-reuse-strategy)
- [Cross-Platform Considerations](#cross-platform-considerations)
- [Package Organization](#package-organization)
- [Usage Examples](#usage-examples)
- [Testing](#testing)
- [Final Polish](#final-polish)

---

## Overview

This design unifies all Nix build outputs into a coherent system that:

1. **Supports multiple platforms** — Build on macOS, Linux, and other Nix-supported systems
2. **Provides complete development environment** — All tools needed for Go development
3. **Offers multiple deployment options** — From simple runners to full VM images
4. **Shares configuration** — Single source of truth for services and profiles
5. **Maintains consistency** — Same behavior across all deployment variants

### Build Outputs Summary

| Output | Platform | Purpose | Isolation | Multi-Arch |
|--------|----------|---------|-----------|------------|
| **Go package** | All | Core binary | None | ✅ x86_64, aarch64 |
| **Development shell** | All | Local development | None | ✅ x86_64, aarch64 |
| **Test origin runner** | All | Local testing | None | ✅ x86_64, aarch64 |
| **Main binary container** | All (build), Linux (run) | Simple deployment | Container | ✅ x86_64, aarch64 |
| **Test origin container** | All (build), Linux (run) | Basic origin server | Container | ✅ x86_64, aarch64 |
| **Test origin enhanced container** | Linux only | Production-like origin | Container + systemd | ✅ x86_64, aarch64 |
| **Test origin MicroVM** | Linux only | High-performance origin | Full VM | ⚠️ x86_64 (aarch64 TBD) |
| **Test origin ISO** | Linux only | Traditional VM deployment | Full VM | ⚠️ x86_64 (aarch64 TBD) |
| **Swarm client container** | All (build), Linux (run) | Client profiles | Container | ✅ x86_64, aarch64 |

**Multi-Architecture Notes**:
- **x86_64-linux**: Full support for all outputs
- **aarch64-linux**: Full support for containers and packages (Graviton, Raspberry Pi)
- **x86_64-darwin / aarch64-darwin**: Build support for containers, full support for packages and runners

---

## Goals

### Primary Goals

1. **Cross-Platform Support**
   - Build packages on macOS (Darwin) and Linux
   - Build containers on all supported systems
   - Gracefully handle platform-specific limitations
   - Clear error messages for unsupported platforms

2. **Development Shell**
   - Verify `nix develop` includes all necessary Go development tools
   - Ensure `gopls` (Go language server) is available
   - Include `curl` for testing HTTP endpoints
   - Document all available tools

3. **Main Binary Container**
   - OCI container for `go-ffmpeg-hls-swarm` binary
   - Minimal, production-ready image
   - Can be loaded into Docker/Podman
   - Follows OCI best practices

4. **Test Origin Deployment Variants**
   - Runner (shell scripts) — All platforms
   - Basic container (shell scripts) — All platforms
   - Enhanced container (systemd services) — Linux only
   - MicroVM (full VM isolation) — Linux only, requires KVM
   - ISO image (bootable) — Linux only

5. **DRY Configuration**
   - Single NixOS module shared across enhanced container, MicroVM, and ISO
   - Profile system works for all deployment types
   - Consistent behavior across all variants

### Secondary Goals

6. **Documentation** — Clear usage examples for each build output
7. **Testing** — Automated tests for all build variants
8. **CI/CD** — Build all variants in CI (where platform allows)

---

## Current State

### What Works Cross-Platform

✅ **Go package** — Builds on all Nix-supported systems
✅ **Development shell** — Works on macOS and Linux
✅ **Test origin runner** — Works on macOS and Linux
✅ **Basic containers** — Build on all systems (but may need Linux to run)

### What's Linux-Only

❌ **MicroVM** — Requires KVM (Linux only)
❌ **Enhanced containers** — Systemd in containers requires Linux
❌ **ISO images** — Requires NixOS (Linux only)

### Current Configuration Structure

```
nix/
├── lib.nix                # Shared metadata and helpers
├── package.nix           # Go package build
├── shell.nix             # Development shell
├── apps.nix              # App definitions
├── checks.nix            # Go checks
├── container.nix         # ❌ MISSING: Main binary container
├── swarm-client/
│   ├── container.nix     # ✅ Swarm client container
│   └── ...
└── test-origin/
    ├── default.nix       # Entry point
    ├── config.nix        # Profile-based configuration
    ├── config/           # Configuration modules
    ├── ffmpeg.nix        # FFmpeg script generation
    ├── nginx.nix         # Nginx config generation
    ├── runner.nix         # ✅ Local runner (shell scripts)
    ├── container.nix     # ✅ Basic container (shell scripts)
    ├── container-enhanced.nix  # ❌ MISSING: Enhanced container (systemd)
    ├── nixos-module.nix  # ✅ NixOS systemd services (used by MicroVM)
    ├── microvm.nix       # ✅ MicroVM wrapper
    ├── iso.nix           # ❌ MISSING: ISO image builder
    └── sysctl.nix        # Kernel tuning
```

### Missing Components

1. ❌ **Main binary container** (`nix/container.nix`)
2. ❌ **Enhanced test-origin container** (`nix/test-origin/container-enhanced.nix`)
3. ❌ **ISO image builder** (`nix/test-origin/iso.nix`)
4. ⚠️ **Cross-platform package organization** (needs better structure)

---

## Architecture Overview

### Build Output Hierarchy

```
┌─────────────────────────────────────────────────────────────┐
│                    Universal Builds                         │
│  (All platforms: Linux, macOS, etc.)                        │
├─────────────────────────────────────────────────────────────┤
│  • go-ffmpeg-hls-swarm (package)                           │
│  • Development shell                                        │
│  • Test origin runner                                       │
│  • Main binary container                                    │
│  • Test origin basic container                              │
│  • Swarm client container                                   │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                  Linux-Only Builds                          │
│  (Requires Linux kernel features)                          │
├─────────────────────────────────────────────────────────────┤
│  • Test origin enhanced container (systemd)                 │
│  • Test origin MicroVM (KVM)                                │
│  • Test origin ISO (NixOS)                                   │
└─────────────────────────────────────────────────────────────┘
```

### Configuration Sharing

```
┌─────────────────────────────────────────────────────────────┐
│              Shared Configuration Layer                     │
├─────────────────────────────────────────────────────────────┤
│  config.nix ──────┐                                        │
│  config/          │                                        │
│    ├── base.nix  │                                        │
│    ├── profiles.nix                                        │
│    ├── derived.nix                                         │
│    └── cache.nix                                           │
└─────────────────────────────────────────────────────────────┘
         │
         ├─────────────────┬─────────────────┬───────────────┐
         ▼                 ▼                 ▼               ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   Runner     │  │   Container   │  │  Enhanced    │  │   MicroVM   │
│  (scripts)   │  │   (scripts)   │  │  Container   │  │   (systemd) │
│              │  │              │  │  (systemd)   │  │             │
│  ffmpeg.nix  │  │  ffmpeg.nix  │  │  nixos-module│  │  nixos-module│
│  nginx.nix   │  │  nginx.nix   │  │              │  │              │
└──────────────┘  └──────────────┘  └──────────────┘  └──────────────┘
                                                              │
                                                              ▼
                                                       ┌──────────────┐
                                                       │     ISO      │
                                                       │   (systemd)  │
                                                       │  nixos-module│
                                                       │  + Cloud-Init │
                                                       └──────────────┘
```

### User Experience Flow

```
┌─────────────────────────────────────────────────────────────┐
│                    User Entry Points                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌──────────────────┐      ┌──────────────────┐          │
│  │  nix run .#up    │      │ nix flake check  │          │
│  │  (Unified CLI)   │      │  (Validation)     │          │
│  └──────────────────┘      └──────────────────┘          │
│         │                           │                       │
│         │                           │                       │
│         ▼                           ▼                       │
│  ┌──────────────────────────────────────────┐             │
│  │      Profile & Type Selection             │             │
│  │  • Profile: default, low-latency, etc.    │             │
│  │  • Type: runner, container, vm           │             │
│  └──────────────────────────────────────────┘             │
│         │                           │                       │
│         ▼                           ▼                       │
│  ┌──────────────────┐      ┌──────────────────┐          │
│  │  Deployment      │      │  Test Suite      │          │
│  │  Execution       │      │  Execution       │          │
│  └──────────────────┘      └──────────────────┘          │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

### Multi-Architecture Support

```
┌─────────────────────────────────────────────────────────────┐
│              Supported Architectures                        │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  x86_64-linux  ──────┐                                     │
│  aarch64-linux ──────┼──►  Full Container Support         │
│  x86_64-darwin ──────┤     (buildLayeredImage)             │
│  aarch64-darwin ─────┘                                     │
│                                                             │
│  x86_64-linux  ──────┐                                     │
│  aarch64-linux ──────┼──►  Full Package Support           │
│  x86_64-darwin ──────┤     (buildGoModule)                 │
│  aarch64-darwin ─────┘                                     │
│                                                             │
│  x86_64-linux  ──────►  MicroVM & ISO Support            │
│  aarch64-linux ──────►  TBD (requires framework support) │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## Design Decisions

### 1. Main Binary Container

**Decision**: Create `nix/container.nix` for the main `go-ffmpeg-hls-swarm` binary with environment variable support.

**Rationale**:
- Keeps container definitions modular
- Follows existing pattern (`swarm-client/container.nix`, `test-origin/container.nix`)
- Environment variables make containers "industry standard" for Kubernetes, Nomad, etc.
- Wrapper script maps env vars to CLI flags for maximum flexibility

**Entrypoint**: Wrapper script that:
- Accepts environment variables (e.g., `SWARM_CLIENTS=100` → `-clients 100`)
- Falls back to CLI arguments if env vars not set
- Uses Go library like `cleanenv` or simple shell script for mapping
- Provides clear error messages for required variables

**Pattern** (following `swarm-client/container.nix`):
```nix
entrypoint = pkgs.writeShellApplication {
  name = "swarm-entrypoint";
  runtimeInputs = [ package pkgs.ffmpeg-full ];
  text = ''
    set -euo pipefail

    # Map environment variables to CLI flags
    CLIENTS="''${CLIENTS:-}"
    DURATION="''${DURATION:-}"
    STREAM_URL="''${STREAM_URL:-}"

    # Build command from env vars or use provided args
    exec go-ffmpeg-hls-swarm \
      ''${CLIENTS:+-clients "$CLIENTS"} \
      ''${DURATION:+-duration "$DURATION"} \
      "$@"
  '';
};
```

### 2. Enhanced Container Architecture

**Decision**: Use `dockerTools.buildLayeredImage` with NixOS system closure for enhanced container.

**Rationale**:
- Can reuse same NixOS module as MicroVM
- Works with standard container runtimes
- Maintains consistency across deployment variants
- **Layered images reduce download times** by caching Nix store paths as individual layers
- Significantly improves user experience for large images

**Alternative Considered**: `systemd-nspawn` or `nixos-container`
- ❌ Less portable (requires NixOS host)
- ❌ More complex setup

**Note**: All containers use `buildLayeredImage` for optimal layer caching.

### 3. ISO Image Generation

**Decision**: Use `nixos-generate` / `nixosSystem` with ISO image module.

**Rationale**:
- Standard NixOS approach
- Produces bootable ISO images
- Compatible with all VM platforms (Proxmox, VMware, VirtualBox, QEMU)

### 4. Cross-Platform Build Strategy

**Decision**: Use `lib.optionalAttrs` and `lib.genAttrs` for DRY package generation, supporting multiple architectures.

**Pattern** (DRY profile generation):
```nix
# Generate packages from profile list
let
  profileNames = [ "default" "low-latency" "stress-test" "logged" "debug" ];

  # Generate runner packages for all profiles
  testOriginRunners = lib.genAttrs profileNames (name:
    testOriginProfiles.${name}.runner
  );

  # Generate container packages for all profiles
  testOriginContainers = lib.genAttrs profileNames (name:
    testOriginProfiles.${name}.container
  );

  # Generate MicroVM packages (Linux only)
  testOriginVMs = lib.genAttrs profileNames (name:
    testOriginProfiles.${name}.microvm.vm or null
  );
in
packages = {
  # Universal packages (all platforms)
  ${meta.pname} = package;
  go-ffmpeg-hls-swarm-container = mainContainer;

  # Generated from profile list
} // testOriginRunners
  // testOriginContainers
  // lib.optionalAttrs pkgs.stdenv.isLinux {
    # Linux-only packages
    test-origin-container-enhanced = testOriginProfiles.default.containerEnhanced;
    test-origin-iso = testOriginProfiles.default.iso;
  } // lib.optionalAttrs pkgs.stdenv.isLinux {
    # Generated MicroVM packages (Linux only)
  } // lib.genAttrs (map (n: "test-origin-vm-${n}") profileNames) (name:
    let profileName = lib.removePrefix "test-origin-vm-" name;
    in testOriginProfiles.${profileName}.microvm.vm or null
  );
```

**Multi-Architecture Support**:
```nix
# Support multiple architectures
supportedSystems = [
  "x86_64-linux"
  "aarch64-linux"  # ARM64 (Graviton, Raspberry Pi)
  "x86_64-darwin"  # macOS Intel
  "aarch64-darwin" # macOS Apple Silicon
];

# Use platforms.unix for broader compatibility
packages = lib.optionalAttrs (lib.elem pkgs.stdenv.hostPlatform.system [
  "x86_64-linux"
  "aarch64-linux"
]) {
  # Linux-specific packages
};
```

**Rationale**:
- **DRY principle**: Generate packages from profile lists instead of manual definitions
- **Multi-arch support**: Enable ARM64 (aarch64-linux) for cloud (Graviton) and edge (Raspberry Pi)
- Clear separation of platform-specific vs. universal packages
- Graceful degradation on unsupported platforms
- Follows Nix idioms

### 5. Configuration Reuse (Single Source of Truth for Profiles)

**Decision**: Single `nixos-module.nix` shared across enhanced container, MicroVM, and ISO. Single source of truth for profile names with validation.

**Rationale**:
- Single source of truth for service configuration
- Consistent behavior across all deployment types
- Easier maintenance and testing
- **Profile validation**: Typos fail fast with good messages
- **No duplicated lists**: Everything derives from one profile list

**Pattern**:
```nix
# nix/test-origin/config/profiles.nix
# Single source of truth for profile names
{
  profiles = [
    "default"
    "low-latency"
    "4k-abr"
    "stress-test"
    "logged"
    "debug"
    "tap"
    "tap-logged"
  ];

  # Validation: ensure profile exists
  validateProfile = profile:
    if lib.elem profile profiles then
      profile
    else
      throw "Unknown profile '${profile}'. Available: ${lib.concatStringsSep ", " profiles}";
}

# flake.nix
let
  # Import single source of truth
  profileConfig = import ./nix/test-origin/config/profiles.nix;
  profileNames = profileConfig.profiles;

  # Generate all profile variants (derives from single list)
  testOriginProfiles = lib.genAttrs profileNames (name:
    import ./nix/test-origin {
      inherit pkgs lib meta microvm;
      profile = profileConfig.validateProfile name;  # Validates here
    }
  );

  # Unified CLI also uses same list
  upApp = pkgs.writeShellApplication {
    name = "swarm-up";
    text = ''
      # Profiles from single source of truth
      PROFILES=(${lib.concatStringsSep " " (map (p: "\"$p\"") profileNames)})

      # Interactive menu uses same list
      if command -v gum >/dev/null 2>&1; then
        PROFILE=$(gum choose "''${PROFILES[@]}" --header "Select Profile:")
      else
        select profile in "''${PROFILES[@]}"; do
          PROFILE="$profile"
          break
        done
      fi
    '';
  };
in
{
  # All packages derive from same profile list
  packages = lib.genAttrs profileNames (name:
    testOriginProfiles.${name}.runner
  );
}
```

**Validation Benefits**:
1. **Typos fail fast**: `validateProfile` throws clear error with available options
2. **No duplicated lists**: Scripts, apps, packages all use same source
3. **Easy to add profiles**: Add to one list, everything updates
4. **Consistent naming**: Profile names validated at evaluation time

### 6. Development Shell

**Decision**: No changes needed — current shell already has required tools.

**Verification**:
- ✅ `go`, `gopls`, `gotools`, `golangci-lint`, `delve`
- ✅ `curl`, `jq`, `nil`
- ✅ `ffmpeg-full`

**Action**: Document available tools clearly.

### 7. Unified CLI Entry Point (The Happy Path)

**Decision**: Provide unified CLI via Nix apps with a dispatcher pattern that prints what it will do before execution.

**Rationale**:
- **Reduces cognitive load**: Single entry point (`nix run .#up`) instead of remembering 20+ package names
- **Less surprising**: Always prints what it's going to do (profile + deployment type + underlying package/app)
- **Stable default**: `default` profile + `runner` type is the "never breaks" path on all platforms
- **Discoverable**: `--help` prints crisp usage message and examples

**Pattern**:
```nix
# nix/apps.nix
up = mkApp (pkgs.writeShellApplication {
  name = "swarm-up";
  runtimeInputs = [ pkgs.bash ];
  text = ''
    set -euo pipefail

    # Handle --help first
    if [[ "$*" == *"--help"* ]] || [[ "$*" == *"-h"* ]]; then
      cat <<EOF
    go-ffmpeg-hls-swarm - Unified Deployment CLI

    USAGE:
      nix run .#up [profile] [type] [args...]

    EXAMPLES:
      # Default: default profile, runner type (works on all platforms)
      nix run .#up

      # Specific profile and type
      nix run .#up low-latency runner
      nix run .#up default container
      nix run .#up stress vm  # Linux only

    PROFILES:
      default        Standard 2s segments, 720p
      low-latency    1s segments, optimized for speed
      4k-abr         Multi-bitrate 4K streaming
      stress         Maximum throughput configuration
      logged         With buffered segment logging
      debug          Full logging with gzip compression

    TYPES:
      runner         Local shell script (all platforms)
      container      OCI container (Linux to run)
      vm             MicroVM (Linux + KVM only)

    The default (profile=default, type=runner) is the stable, cross-platform path.
    EOF
      exit 0
    fi

    # Parse arguments
    PROFILE="''${1:-default}"
    TYPE="''${2:-runner}"
    shift 2 2>/dev/null || true

    # Resolve underlying package/app
    case "$TYPE" in
      runner)
        UNDERLYING="test-origin-$PROFILE"
        ;;
      container)
        UNDERLYING="test-origin-container"
        ;;
      vm)
        UNDERLYING="test-origin-vm-$PROFILE"
        ;;
      *)
        echo "Error: Unknown type '$TYPE'"
        echo "Valid types: runner, container, vm"
        exit 1
        ;;
    esac

    # Print what we're going to do (dispatcher pattern)
    echo "╔════════════════════════════════════════════════════════════╗"
    echo "║  go-ffmpeg-hls-swarm - Deployment Dispatcher                ║"
    echo "╚════════════════════════════════════════════════════════════╝"
    echo ""
    echo "Profile:        $PROFILE"
    echo "Type:           $TYPE"
    echo "Underlying:     .#$UNDERLYING"
    echo ""
    echo "Executing: nix run .#$UNDERLYING $*"
    echo ""

    # Execute
    exec nix run ".#$UNDERLYING" "$@"
  '';
});
```

**Usage**:
```bash
# Help (people will try this first)
nix run .#up -- --help

# Default: stable path (default profile + runner type)
nix run .#up

# Specify profile and type
nix run .#up -- low-latency runner
nix run .#up -- default container
nix run .#up -- stress vm  # Linux only
```

**Key Principles**:
1. **Always prints what it will do** before execution (dispatcher pattern)
2. **`--help` works first** and provides clear examples
3. **Default is stable**: `default` profile + `runner` type works on all platforms
4. **No surprises**: Platform limitations are checked and reported clearly

### 8. Nix Flake Check Integration (Tiered Checks)

**Decision**: Integrate `scripts/nix-tests/` into `nix flake check` with tiered checks to avoid "building the world" surprises.

**Rationale**:
- **Valuable but not overwhelming**: `nix flake check` should be fast locally, comprehensive in CI
- **Less surprising**: Contributors won't be surprised by long build times
- **Tiered approach**: Quick checks by default, full checks opt-in or CI-only

**Pattern**:
```nix
# nix/checks.nix
let
  # Quick checks: fast validation (fmt/vet/lint/unit tests + cheap Nix eval)
  quick = {
    format = goChecks.format;
    vet = goChecks.vet;
    lint = goChecks.lint;
    test = goChecks.test;

    # Cheap Nix evaluation checks (no builds)
    nix-eval = pkgs.writeShellApplication {
      name = "nix-eval";
      runtimeInputs = [ pkgs.nix ];
      text = ''
        # Just verify packages can be evaluated (fast)
        nix eval .#packages --json >/dev/null
        echo "✓ All packages evaluate successfully"
      '';
    };

    shellcheck = pkgs.writeShellApplication {
      name = "shellcheck";
      runtimeInputs = [ pkgs.shellcheck ];
      text = "exec ${./scripts/nix-tests/shellcheck.sh}";
    };
  };

  # Build checks: build key packages/containers (default profile only)
  build = quick // {
    build-core = package;  # Core Go binary

    build-default-runner = testOriginProfiles.default.runner;
    build-default-container = testOriginProfiles.default.container;
    build-main-container = mainContainer;
  };

  # Full checks: build all profiles/variants (CI-only / opt-in)
  full = build // {
    # All profile builds (via test scripts)
    nix-tests = pkgs.writeShellApplication {
      name = "nix-tests";
      runtimeInputs = [ pkgs.bash pkgs.nix ];
      text = ''
        exec ${./scripts/nix-tests/test-all.sh}
      '';
    };
  };
in
{
  # Default: quick checks (fast, local-friendly)
  default = quick;

  # Explicit tiers
  quick = quick;
  build = build;
  full = full;
}
```

**Usage**:
```bash
# Default: quick checks only (fast, ~30 seconds)
nix flake check

# Explicit quick checks
nix flake check .#checks.quick

# Build key packages (default profile only, ~5-10 minutes)
nix flake check .#checks.build

# Full checks (all profiles/variants, CI-only, ~45-60 minutes)
nix flake check .#checks.full
```

**CI Configuration**:
```yaml
# .github/workflows/ci.yml
jobs:
  quick-checks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - run: nix flake check .#checks.quick

  full-checks:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - run: nix flake check .#checks.full
```

**Key Principles**:
1. **Default is fast**: `nix flake check` runs quick checks (~30 seconds)
2. **Explicit tiers**: Users can opt into build/full checks when needed
3. **CI runs full**: CI pipeline runs comprehensive checks
4. **No surprises**: Contributors know what to expect

### 9. Cloud-Init Support for ISO and MicroVM

**Decision**: Add optional Cloud-Init module for ISO and MicroVM variants.

**Rationale**:
- **Common requirement**: Users need to inject SSH keys, network configs without rebuilding
- **Large audience adoption**: Essential for homelabs and cloud environments
- **Flexibility**: Optional — can be disabled for simple deployments

**Pattern**:
```nix
# nix/test-origin/iso.nix
modules = [
  # ... existing modules ...

  # Optional Cloud-Init support
  ({ lib, ... }: lib.mkIf (config.cloudInit.enable or false) {
    # Cloud-Init configuration
    systemd.services.cloud-init = {
      enable = true;
      wantedBy = [ "multi-user.target" ];
    };

    # Allow user data injection
    cloud-init = {
      enable = true;
      userData = config.cloudInit.userData or "";
    };
  })
];
```

**Usage**:
```bash
# Build ISO with Cloud-Init
nix build .#test-origin-iso --arg cloudInit '{ enable = true; userData = "..."; }'

# Or via environment variable
CLOUD_INIT_USER_DATA="$(cat user-data.yml)" nix build .#test-origin-iso
```

### 10. Interactive Unified CLI (TTY-Aware, No Dependency Surprises)

**Decision**: Make `nix run .#up` interactive when no arguments provided, but only in TTY environments. Auto-detect TTY and fallback gracefully.

**Rationale**:
- **First-run discoverability**: Newcomers can visually see available options
- **No dependency surprises**: Auto-detects TTY, falls back to bash `select` if `gum` missing
- **CI/non-TTY friendly**: Never prompts in non-TTY environments, defaults to stable path
- **Less surprising**: Users understand when interactive mode activates

**Implementation**:
```nix
# nix/apps.nix
up = mkApp (pkgs.writeShellApplication {
  name = "swarm-up";
  runtimeInputs = [ pkgs.bash ];
  text = ''
    set -euo pipefail

    # Auto-detect TTY
    IS_TTY=0
    if [[ -t 0 ]] && [[ -t 1 ]]; then
      IS_TTY=1
    fi

    # If no arguments and not a TTY, use defaults (CI/non-interactive)
    if [[ $# -eq 0 ]] && [[ $IS_TTY -eq 0 ]]; then
      echo "Non-interactive mode: using defaults (profile=default, type=runner)"
      PROFILE="default"
      TYPE="runner"
    # If no arguments and TTY, show interactive menu
    elif [[ $# -eq 0 ]] && [[ $IS_TTY -eq 1 ]]; then
      echo "╔════════════════════════════════════════════════════════════╗"
      echo "║     go-ffmpeg-hls-swarm - Interactive Deployment         ║"
      echo "╚════════════════════════════════════════════════════════════╝"
      echo ""

      # Try gum first, fallback to bash select
      if command -v gum >/dev/null 2>&1; then
        PROFILE=$(gum choose \
          "default" \
          "low-latency" \
          "4k-abr" \
          "stress" \
          "logged" \
          "debug" \
          --header "Select Profile:")

        TYPE=$(gum choose \
          "runner" \
          "container" \
          "vm" \
          --header "Select Deployment Type:")
      else
        # Fallback to bash select (no external dependency)
        echo "Select Profile:"
        select profile in default low-latency 4k-abr stress logged debug; do
          PROFILE="$profile"
          break
        done

        echo ""
        echo "Select Deployment Type:"
        select type in runner container vm; do
          TYPE="$type"
          break
        done
      fi
    else
      # Use provided arguments
      PROFILE="''${1:-default}"
      TYPE="''${2:-runner}"
      shift 2 2>/dev/null || true
    fi

    # Continue with dispatcher pattern (print what we'll do, then execute)
    # ... (rest of dispatcher logic from Design Decision 7)
  '';
});
```

**Behavior**:
- **TTY + no args**: Interactive menu (gum if available, bash select otherwise)
- **Non-TTY + no args**: Defaults to `default` profile + `runner` type (CI-friendly)
- **With args**: Always uses provided arguments (no prompting)

**Documentation Note**: In README, mention "Interactive only when run in a terminal; otherwise behaves like a normal CLI with sensible defaults."

### 11. Enhanced Container Metadata

**Decision**: Include comprehensive OCI labels in all containers for discoverability.

**Rationale**:
- **Docker inspect discoverability**: Users can find repo/docs directly from image
- **Industry standard**: Follows OCI best practices
- **Documentation links**: Direct links to relevant docs

**Pattern**:
```nix
Labels = {
  "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm";
  "org.opencontainers.image.description" = "HLS load testing with FFmpeg process orchestration";
  "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
  "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/README.md";
  "org.opencontainers.image.version" = "0.1.0";
  "org.opencontainers.image.vendor" = "randomizedcoder";
  "org.opencontainers.image.licenses" = "MIT";
  "swarm.profile" = config._profile.name;
  "swarm.deployment-type" = "container";
};
```

### 12. Docker Compose / Justfile Support

**Decision**: Provide `docker-compose.yaml` and/or `Justfile` to simplify enhanced container usage.

**Rationale**:
- **Hides complexity**: Users don't need to remember `--cap-add SYS_ADMIN --tmpfs /run` flags
- **One-command experience**: `docker-compose up` or `just enhanced-origin`
- **Reproducible**: Same flags every time

**Implementation**:
```yaml
# docker-compose.yaml
version: '3.8'

services:
  test-origin-enhanced:
    image: go-ffmpeg-hls-swarm-test-origin-enhanced:latest
    build:
      context: .
      dockerfile: Dockerfile.enhanced  # Or use nix build
    ports:
      - "8080:8080"
      - "9100:9100"  # Metrics
      - "9113:9113"  # Nginx exporter
    cap_add:
      - SYS_ADMIN
    tmpfs:
      - /tmp
      - /run
      - /run/lock
    environment:
      - PROFILE=default
```

**Justfile**:
```just
# Justfile
default:
    @just --list

# Enhanced container (one command)
enhanced-origin:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build .#test-origin-container-enhanced
    docker load < ./result
    docker run --rm -p 8080:8080 \
      --cap-add SYS_ADMIN \
      --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
      go-ffmpeg-hls-swarm-test-origin-enhanced:latest
```

### 13. Shell Autocompletion (Auto-Generated from Single Source of Truth)

**Decision**: Provide shell completion scripts that are automatically generated from the single source of truth profile list.

**Rationale**:
- **Faster workflow**: Tab completion reduces typing
- **Discoverability**: Shows available options as user types
- **Professional touch**: Expected in modern CLI tools
- **Prevents typos**: Generated from same source as packages/apps, so always in sync
- **No manual maintenance**: Add profile to one list, completion updates automatically

**Implementation**:

**Nix App to Generate Completion**:
```nix
# nix/apps.nix
generate-completion = mkApp (pkgs.writeShellApplication {
  name = "generate-completion";
  runtimeInputs = [ pkgs.bash ];
  text = ''
    set -euo pipefail

    # Extract profiles from single source of truth
    PROFILES=$(nix eval --impure --expr '
      let
        profileConfig = import ./nix/test-origin/config/profiles.nix;
      in
      builtins.concatStringsSep " " profileConfig.profiles
    ' --raw)

    TYPES="runner container vm"
    OUTPUT_DIR="''${1:-./scripts/completion}"

    # Generate bash completion
    cat > "$OUTPUT_DIR/bash-completion.sh" <<EOF
    # Auto-generated from single source of truth
    # Do not edit manually - run: nix run .#generate-completion

    _swarm_up() {
        local cur prev
        COMPREPLY=()
        cur="''${COMP_WORDS[COMP_CWORD]}"
        prev="''${COMP_WORDS[COMP_CWORD-1]}"

        local profiles="$PROFILES"
        local types="$TYPES"

        case "$prev" in
            up)
                COMPREPLY=(\$(compgen -W "\$profiles" -- "\$cur"))
                ;;
            $PROFILES)
                COMPREPLY=(\$(compgen -W "\$types" -- "\$cur"))
                ;;
        esac
    }
    complete -F _swarm_up nix run .#up
    EOF

    # Generate zsh completion
    cat > "$OUTPUT_DIR/zsh-completion.sh" <<EOF
    # Auto-generated from single source of truth
    # Do not edit manually - run: nix run .#generate-completion

    _swarm_up() {
        local profiles=($PROFILES)
        local types=($TYPES)

        case $CURRENT in
            2)
                _describe 'profiles' profiles
                ;;
            3)
                _describe 'types' types
                ;;
        esac
    }

    compdef _swarm_up 'nix run .#up'
    EOF

    echo "✓ Generated completion scripts in $OUTPUT_DIR"
    echo "  - bash-completion.sh"
    echo "  - zsh-completion.sh"
    echo ""
    echo "To install:"
    echo "  # Bash"
    echo "  source $OUTPUT_DIR/bash-completion.sh"
    echo ""
    echo "  # Zsh"
    echo "  source $OUTPUT_DIR/zsh-completion.sh"
  '';
});
```

**Usage**:
```bash
# Generate completion scripts from single source of truth
nix run .#generate-completion

# Install (add to ~/.bashrc or ~/.zshrc)
source ./scripts/completion/bash-completion.sh  # Bash
source ./scripts/completion/zsh-completion.sh    # Zsh
```

**Benefits**:
1. **Always in sync**: Profiles come from same source as packages/apps
2. **No typos**: Can't have mismatched profile names
3. **Easy updates**: Add profile to one list, regenerate completion
4. **CI-friendly**: Can generate in CI to verify completion is up-to-date

---

## Implementation Plan

### Phase 1: Main Binary Container

**File**: `nix/container.nix` (NEW)

```nix
# OCI container image for go-ffmpeg-hls-swarm
#
# Build: nix build .#go-ffmpeg-hls-swarm-container
# Load:  docker load < ./result
# Run:   docker run --rm go-ffmpeg-hls-swarm:latest -clients 10 http://origin:8080/stream.m3u8
#
{ pkgs, lib, package }:

let
  # Wrapper script with environment variable support
  # Maps env vars to CLI flags (e.g., SWARM_CLIENTS=100 → -clients 100)
  entrypoint = pkgs.writeShellApplication {
    name = "swarm-entrypoint";
    runtimeInputs = [ package pkgs.ffmpeg-full ];
    text = ''
      set -euo pipefail

      # Environment variable mapping (following swarm-client pattern)
      # Required variables can be validated here

      # Optional with defaults
      CLIENTS="''${CLIENTS:-}"
      DURATION="''${DURATION:-}"
      RAMP_RATE="''${RAMP_RATE:-}"
      METRICS_PORT="''${METRICS_PORT:-9100}"
      LOG_LEVEL="''${LOG_LEVEL:-info}"

      # Build command from env vars or use provided args
      # If env vars are set, use them; otherwise pass through CLI args
      ARGS=()

      [ -n "$CLIENTS" ] && ARGS+=(--clients "$CLIENTS")
      [ -n "$DURATION" ] && ARGS+=(--duration "$DURATION")
      [ -n "$RAMP_RATE" ] && ARGS+=(--ramp-rate "$RAMP_RATE")
      [ -n "$METRICS_PORT" ] && ARGS+=(--metrics-port "$METRICS_PORT")
      [ -n "$LOG_LEVEL" ] && ARGS+=(--log-level "$LOG_LEVEL")

      # Execute with env vars or CLI args
      exec ${lib.getExe package} "''${ARGS[@]}" "$@"
    '';
  };
in pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm";
  tag = "latest";

  contents = [
    # Core binary
    package

    # Runtime dependencies
    pkgs.ffmpeg-full
    entrypoint

    # Minimal utilities for debugging
    pkgs.busybox
    pkgs.curl

    # TLS certificates (required for HTTPS streams)
    pkgs.cacert
  ];

  config = {
    Entrypoint = [ "${lib.getExe entrypoint}" ];

    ExposedPorts = {
      "9100/tcp" = {};  # Metrics port (default)
    };

    Env = [
      # TLS certificates
      "SSL_CERT_FILE=${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt"
      "METRICS_PORT=9100"
      "LOG_LEVEL=info"
    ];

    # Healthcheck for container orchestration (Kubernetes, Docker Compose, etc.)
    Healthcheck = {
      Test = [ "CMD" "curl" "-f" "http://localhost:9100/metrics" "||" "exit" "1" ];
      Interval = 30000000000;  # 30 seconds (nanoseconds)
      Timeout = 5000000000;    # 5 seconds
      StartPeriod = 10000000000;  # 10 seconds grace period
      Retries = 3;
    };

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm";
      "org.opencontainers.image.description" = "HLS load testing tool using FFmpeg process swarm";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/README.md";
      "org.opencontainers.image.version" = "0.1.0";
      "org.opencontainers.image.vendor" = "randomizedcoder";
    };
  };

  fakeRootCommands = ''
    mkdir -p /tmp
    chmod 1777 /tmp
  '';

  maxLayers = 100;
}
```

**Integration in flake.nix**:
```nix
packages = {
  ${meta.pname} = package;
  default = package;

  # OCI container for main binary (all platforms can build)
  go-ffmpeg-hls-swarm-container = import ./nix/container.nix {
    inherit pkgs lib;
    package = package;
  };

  # ... existing packages ...
};
```

### Phase 2: Enhanced Test-Origin Container

**File**: `nix/test-origin/container-enhanced.nix` (NEW)

```nix
# Enhanced OCI container with full NixOS systemd services
# Similar to MicroVM but runs in a container
# Uses the same nixos-module.nix for consistency
{ pkgs, lib, config, nixosModule, nixpkgs }:

let
  system = "x86_64-linux";

  # Build NixOS system (same pattern as MicroVM)
  nixos = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      # Minimal NixOS config for container
      ({ lib, ... }: {
        boot.isContainer = true;
        networking.hostName = "hls-origin";
        system.stateVersion = "24.11";
      })

      # Our shared NixOS module
      nixosModule

      # Container-specific overrides
      ({ lib, ... }: {
        services.hls-origin = {
          enable = true;
          config = config;
        };
      })
    ];
  };

  # Extract the system closure
  systemClosure = nixos.config.system.build.toplevel;

in pkgs.dockerTools.buildLayeredImage {
  name = "go-ffmpeg-hls-swarm-test-origin-enhanced";
  tag = "latest";

  # Use the NixOS system closure
  fromImage = systemClosure;

  # Layer optimization
  maxLayers = 100;

  config = {
    Cmd = [ "/init" ];  # NixOS init system
    ExposedPorts = {
      "${toString config.server.port}/tcp" = {};
      "9100/tcp" = {};  # Node exporter
      "9113/tcp" = {};  # Nginx exporter
    };

    # Healthcheck for container orchestration
    # Checks the /health endpoint on the origin server
    Healthcheck = {
      Test = [ "CMD" "curl" "-f" "http://localhost:${toString config.server.port}/health" "||" "exit" "1" ];
      Interval = 30000000000;  # 30 seconds (nanoseconds)
      Timeout = 5000000000;    # 5 seconds
      StartPeriod = 30000000000;  # 30 seconds grace period (systemd needs time to start)
      Retries = 3;
    };

    Labels = {
      "org.opencontainers.image.title" = "go-ffmpeg-hls-swarm-test-origin-enhanced";
      "org.opencontainers.image.description" = "Test HLS origin with full NixOS systemd services";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/docs/TEST_ORIGIN.md";
      "hls.profile" = config._profile.name;
    };
  };
}
```

### Phase 3: ISO Image Builder

**File**: `nix/test-origin/iso.nix` (NEW)

```nix
# ISO image for test origin server
# Bootable image for Proxmox, VMware, VirtualBox, etc.
# Supports optional Cloud-Init for SSH keys and network configuration
{ pkgs, lib, config, nixosModule, nixpkgs, cloudInit ? null }:

let
  system = "x86_64-linux";  # TODO: Support aarch64-linux

  # Build NixOS ISO
  iso = nixpkgs.lib.nixosSystem {
    inherit system;
    modules = [
      # ISO image base configuration
      "${nixpkgs}/nixos/modules/installer/cd-dvd/iso-image.nix"

      # Our shared NixOS module
      nixosModule

      # ISO-specific configuration
      ({ lib, ... }: {
        # Enable HLS origin service
        services.hls-origin = {
          enable = true;
          config = config;
        };

        # Boot configuration
        boot.loader.grub = {
          enable = true;
          version = 2;
          device = "/dev/sda";
        };

        # Networking (DHCP by default, can be configured)
        networking = {
          hostName = "hls-origin";
          useDHCP = true;
          firewall.enable = false;  # Allow all traffic for testing
        };

        # Allow root login for initial setup
        users.users.root.password = "";
        services.getty.autologinUser = "root";

        # SSH for remote access
        services.openssh = {
          enable = true;
          permitRootLogin = "yes";
          passwordAuthentication = true;
        };

        # Optional Cloud-Init support
      } // lib.optionalAttrs (cloudInit != null && cloudInit.enable) {
        # Cloud-Init configuration
        systemd.services.cloud-init = {
          enable = true;
          wantedBy = [ "multi-user.target" ];
        };

        # Cloud-Init user data
        cloud-init = {
          enable = true;
          userData = cloudInit.userData or "";
        };

        # Allow Cloud-Init to configure networking
        networking.useNetworkd = true;
      } // {
        # System version
        system.stateVersion = "24.11";
      })
    ];
  };

in iso.config.system.build.isoImage
```

### Phase 4: Update Test-Origin Default Export

**File**: `nix/test-origin/default.nix`

**Add new exports**:

```nix
in {
  # ... existing exports ...

  # Enhanced container (Linux only)
  containerEnhanced = if pkgs.stdenv.isLinux then
    import ./container-enhanced.nix {
      inherit pkgs lib config nixosModule;
      nixpkgs = pkgs;
    }
  else
    null;

  # ISO image (Linux only)
  iso = if pkgs.stdenv.isLinux then
    import ./iso.nix {
      inherit pkgs lib config nixosModule;
      nixpkgs = pkgs;
    }
  else
    null;
}
```

### Phase 5: Update Flake for Cross-Platform Support

**File**: `flake.nix`

**DRY Package Organization** (using `lib.genAttrs`):

```nix
let
  # Profile names for test origin
  testOriginProfileNames = [
    "default"
    "low-latency"
    "4k-abr"
    "stress-test"
    "logged"
    "debug"
    "tap"
    "tap-logged"
  ];

  # Profile names for swarm client
  swarmClientProfileNames = [
    "default"
    "stress"
    "gentle"
    "burst"
    "extreme"
  ];

  # Generate runner packages for all test origin profiles
  testOriginRunners = lib.genAttrs testOriginProfileNames (name:
    testOriginProfiles.${name}.runner
  );

  # Generate container packages for all test origin profiles
  testOriginContainers = lib.genAttrs testOriginProfileNames (name:
    testOriginProfiles.${name}.container
  );

  # Generate runner packages for all swarm client profiles
  swarmClientRunners = lib.genAttrs swarmClientProfileNames (name:
    swarmClientProfiles.${name}.runner
  );

  # Generate MicroVM packages (Linux only, with prefix)
  testOriginVMs = lib.genAttrs (map (n: "test-origin-vm-${n}") testOriginProfileNames) (fullName:
    let
      profileName = lib.removePrefix "test-origin-vm-" fullName;
    in
    if pkgs.stdenv.isLinux then
      testOriginProfiles.${profileName}.microvm.vm or null
    else
      null
  );
in
packages = {
  # ═══════════════════════════════════════════════════════════════════════
  # Universal Packages (All Platforms)
  # ═══════════════════════════════════════════════════════════════════════
  ${meta.pname} = package;
  default = package;

  # Main binary container (can build on all platforms, run on Linux)
  go-ffmpeg-hls-swarm-container = import ./nix/container.nix {
    inherit pkgs lib;
    package = package;
  };

  # Generated test origin runners (all platforms)
} // testOriginRunners
  // {
    # Generated test origin containers (all platforms)
  } // testOriginContainers
  // {
    # Swarm client container
    swarm-client-container = swarmClientProfiles.default.container;
  } // swarmClientRunners
  // {
    # ═══════════════════════════════════════════════════════════════════════
    # Linux-Only Packages (Requires Linux Kernel Features)
    # ═══════════════════════════════════════════════════════════════════════
  } // lib.optionalAttrs pkgs.stdenv.isLinux {
    # Enhanced test origin container (requires systemd)
    test-origin-container-enhanced = testOriginProfiles.default.containerEnhanced or null;

    # ISO image (requires NixOS)
    test-origin-iso = testOriginProfiles.default.iso or null;
  } // lib.optionalAttrs pkgs.stdenv.isLinux {
    # Generated MicroVM packages (requires KVM)
  } // testOriginVMs;
```

**Multi-Architecture Support**:

```nix
# flake.nix
supportedSystems = [
  "x86_64-linux"
  "aarch64-linux"   # ARM64 support
  "x86_64-darwin"
  "aarch64-darwin"
];

outputs = { self, nixpkgs, flake-utils, microvm }:
  flake-utils.lib.eachSystem supportedSystems (system:
    let
      pkgs = nixpkgs.legacyPackages.${system};
      # ... rest of configuration
    in
    {
      packages = {
        # Packages available on all supported systems
      } // lib.optionalAttrs (lib.elem system [ "x86_64-linux" "aarch64-linux" ]) {
        # Linux-specific packages (both x86_64 and aarch64)
      };
    }
  );
```

### Phase 6: Update Test Scripts

**File**: `scripts/nix-tests/test-containers.sh`

Add tests for all containers:

```bash
# Main binary container
log_test "Building go-ffmpeg-hls-swarm-container..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --no-link 2>&1; then
    test_pass "go-ffmpeg-hls-swarm-container"
else
    test_fail "go-ffmpeg-hls-swarm-container" "Build failed"
fi

# Enhanced container (Linux only)
if is_linux; then
    log_test "Building test-origin-container-enhanced..."
    if nix build ".#packages.$SYSTEM.test-origin-container-enhanced" --no-link 2>&1; then
        test_pass "test-origin-container-enhanced"
    else
        test_fail "test-origin-container-enhanced" "Build failed"
    fi
else
    test_skip "test-origin-container-enhanced" "Requires Linux"
fi
```

**File**: `scripts/nix-tests/test-iso.sh` (NEW)

```bash
#!/usr/bin/env bash
# Test ISO image builds

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing ISO image builds for $SYSTEM"

if ! is_linux; then
    log_warn "ISO images require Linux"
    test_skip "all-isos" "Not on Linux"
    print_summary
    exit 0
fi

log_test "Building test-origin-iso..."
if nix build ".#packages.$SYSTEM.test-origin-iso" --no-link 2>&1; then
    test_pass "test-origin-iso"
else
    test_fail "test-origin-iso" "Build failed"
fi

print_summary
```

### Phase 7: Documentation

1. **Update README.md** — Add comprehensive Nix builds section
2. **Update flake.nix comments** — Document all packages and platform support
3. **Create deployment guide** — Document all deployment options

---

## Configuration Reuse Strategy

### Shared Components

All deployment variants share:

1. **`config.nix`** — Profile-based configuration
2. **`nixos-module.nix`** — Systemd services (FFmpeg, Nginx, exporters) — used by enhanced container, MicroVM, and ISO
3. **`ffmpeg.nix`** — FFmpeg script generation (used by runner and basic container)
4. **`nginx.nix`** — Nginx config generation (used by runner, basic container, and NixOS module)

### Variant-Specific Components

| Component | Runner | Basic Container | Enhanced Container | MicroVM | ISO |
|-----------|--------|----------------|-------------------|---------|-----|
| **Config Source** | `config.nix` | `config.nix` | `config.nix` | `config.nix` | `config.nix` |
| **FFmpeg** | `ffmpeg.nix` script | `ffmpeg.nix` script | `nixos-module.nix` (systemd) | `nixos-module.nix` (systemd) | `nixos-module.nix` (systemd) |
| **Nginx** | `nginx.nix` config | `nginx.nix` config | `nixos-module.nix` (systemd) | `nixos-module.nix` (systemd) | `nixos-module.nix` (systemd) |
| **Entrypoint** | Shell script | Shell script | systemd init | MicroVM runner | GRUB boot |
| **Isolation** | None | Container | Container | Full VM | Full VM |

### DRY Pattern

```nix
# nix/test-origin/default.nix
let
  # Shared configuration (profile-based)
  config = import ./config.nix { ... };

  # Shared NixOS module (systemd services)
  nixosModule = import ./nixos-module.nix { inherit config ffmpeg nginx; };

  # Shared FFmpeg/Nginx (for runner and basic container)
  ffmpeg = import ./ffmpeg.nix { inherit pkgs lib config; };
  nginx = import ./nginx.nix { inherit pkgs lib config; };

  # Variant-specific wrappers
  runner = import ./runner.nix { inherit config ffmpeg nginx; };
  container = import ./container.nix { inherit config ffmpeg nginx; };
  containerEnhanced = import ./container-enhanced.nix { inherit config nixosModule; ... };
  microvm = import ./microvm.nix { inherit config nixosModule; ... };
  iso = import ./iso.nix { inherit config nixosModule; ... };
in {
  inherit config nixosModule ffmpeg nginx;
  inherit runner container containerEnhanced microvm iso;
}
```

---

## Cross-Platform Considerations

### Platform Support Matrix

| Feature | x86_64-linux | aarch64-linux | x86_64-darwin | aarch64-darwin | Notes |
|---------|--------------|---------------|---------------|----------------|-------|
| **Go package** | ✅ | ✅ | ✅ | ✅ | Universal |
| **Development shell** | ✅ | ✅ | ✅ | ✅ | Universal |
| **Test origin runner** | ✅ | ✅ | ✅ | ✅ | Universal |
| **Main binary container** | ✅ B&R | ✅ B&R | ✅ Build | ✅ Build | Run requires Linux |
| **Test origin basic container** | ✅ B&R | ✅ B&R | ✅ Build | ✅ Build | Run requires Linux |
| **Test origin enhanced container** | ✅ B&R | ✅ B&R | ❌ | ❌ | Requires systemd (Linux) |
| **Test origin MicroVM** | ✅ | ⚠️ TBD | ❌ | ❌ | Requires KVM (Linux) |
| **Test origin ISO** | ✅ | ⚠️ TBD | ❌ | ❌ | Requires NixOS (Linux) |
| **Swarm client container** | ✅ B&R | ✅ B&R | ✅ Build | ✅ Build | Run requires Linux |

**Legend**: B&R = Build & Run, TBD = To Be Determined

**Multi-Architecture Support**:
- **aarch64-linux** (ARM64): Full support for containers and packages
  - AWS Graviton instances
  - Raspberry Pi 4/5
  - Other ARM64 servers
- **MicroVM/ISO on ARM64**: Requires MicroVM framework support (TBD)

### Handling Platform Limitations (Explicit and Friendly)

**Decision**: Don't expose null derivations. Either omit attributes entirely or provide helpful apps that explain platform requirements.

**Rationale**:
- **Less surprising**: Users don't see confusing null values
- **Friendly errors**: Clear messages explain what's needed
- **Early feedback**: Platform limits are obvious before attempting builds

**Pattern 1: Omit Attributes (Preferred for packages)**:
```nix
packages = {
  # Universal packages (all platforms)
  ${meta.pname} = package;
  go-ffmpeg-hls-swarm-container = mainContainer;
  test-origin = testOriginProfiles.default.runner;
  test-origin-container = testOriginProfiles.default.container;

} // lib.optionalAttrs pkgs.stdenv.isLinux {
  # Linux-only packages (only present on Linux)
  test-origin-container-enhanced = testOriginProfiles.default.containerEnhanced;
  test-origin-iso = testOriginProfiles.default.iso;
  test-origin-vm = testOriginProfiles.default.microvm.vm;
};
```

**Pattern 2: Helpful Apps (For discoverability)**:
```nix
apps = {
  # ... universal apps ...

  # Linux-only apps with helpful messages
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  test-origin-vm = {
    type = "app";
    program = "${testOriginProfiles.default.microvm.runScript}";
  };
} // lib.optionalAttrs (!pkgs.stdenv.isLinux) {
  # On non-Linux, provide helpful app instead of failing
  test-origin-vm = {
    type = "app";
    program = lib.getExe (pkgs.writeShellApplication {
      name = "test-origin-vm-unavailable";
      text = ''
        echo "Error: MicroVM deployment requires Linux with KVM support."
        echo ""
        echo "This target is not available on $(uname)."
        echo ""
        echo "Alternatives:"
        echo "  • Use 'runner' type: nix run .#up -- default runner"
        echo "  • Use 'container' type: nix run .#up -- default container"
        echo "  • Build on Linux: nix build .#packages.x86_64-linux.test-origin-vm"
        exit 1
      '';
    });
  };
};
```

**In Unified CLI (`.#up`)**:
```bash
# Check platform before attempting VM
if [[ "$TYPE" == "vm" ]] && ! is_linux; then
  echo "Error: VM deployment requires Linux with KVM support."
  echo ""
  echo "You're on $(uname). Try one of these instead:"
  echo "  • Runner: nix run .#up -- $PROFILE runner"
  echo "  • Container: nix run .#up -- $PROFILE container"
  exit 1
fi
```

**Key Principles**:
1. **Omit, don't null**: Linux-only packages simply don't exist on other platforms
2. **Helpful apps**: Provide apps that explain requirements instead of failing silently
3. **Early feedback**: Check platform in CLI before attempting builds
4. **Clear alternatives**: Suggest what works on user's platform

### macOS-Specific Notes

- **Containers**: Can build but cannot run (requires Linux kernel)
- **MicroVM**: Not available (requires KVM)
- **ISO**: Not available (requires NixOS)
- **Development**: Full support (Go, tools, runner)

**Recommendation for macOS users**:
- Use `test-origin` runner for local development
- Build containers on macOS, run on Linux
- Use `test-origin-iso` for VM deployment (build on Linux, deploy anywhere)

---

## User Experience & Accessibility

### Deployment Variants Comparison

To help users choose the right deployment type, here's a scannable comparison table:

| Deployment Type | Isolation | Performance | Platform | Best For |
|----------------|-----------|-------------|----------|----------|
| **Runner** | None | Highest | All | Quick local Go/FFmpeg dev |
| **Container** | Process | High | Linux | CI/CD and Cloud (K8s) |
| **Enhanced Container** | Process + systemd | High | Linux | Production-like testing |
| **MicroVM** | Hardware | Medium | Linux (KVM) | Realistic network/kernel testing |
| **ISO** | Hardware | Medium | Bare Metal | Permanent test-lab hardware |

### Quick Start Guide

**For newcomers**, the recommended path is:

1. **🚀 Zero-Config Demo** (No Install):
   ```bash
   # Single client test - immediate gratification
   ffmpeg -i input.mp4 -c:v libx264 -c:a aac -f hls -hls_time 2 stream.m3u8
   ```

2. **One-Command Dev Environment**:
   ```bash
   nix develop  # Provides: Go, FFmpeg, gopls, curl, jq, nil, etc.
   ```

3. **Simple Load Test**:
   ```bash
   nix run .#up -- <URL>  # Interactive menu if no args
   ```

4. **Test Local Setup**:
   ```bash
   make test-origin  # Or: nix run .#test-origin
   ```

5. **Realistic Origin Stress** (Linux only):
   ```bash
   make microvm-origin  # Or: nix run .#test-origin-vm
   ```

### Build Output Map

**My Goal is...** | **Run this Command** | **Requirements**
-----------------|---------------------|------------------
Simple Load Test | `nix run .#up -- <URL>` | Any OS with Nix
Test my local setup | `make test-origin` | Any OS with Nix
Realistic Origin Stress | `make microvm-origin` | Linux + KVM
Production-like Container | `docker-compose up` | Linux + Docker
Permanent Test Lab | `nix build .#test-origin-iso` | Linux

### Project Structure

**Folder Legend**:

- **`nix/`** — Nix build definitions
  - `package.nix` — Core Go binary build
  - `container.nix` — Main binary container
  - `shell.nix` — Development environment
  - `test-origin/` — Test origin server configurations
  - `swarm-client/` — Swarm client configurations

- **`cmd/`** — Go application entry points
  - `go-ffmpeg-hls-swarm/main.go` — Main application

- **`scripts/`** — Utility scripts
  - `nix-tests/` — Automated Nix build tests
  - `microvm/` — MicroVM management scripts
  - `network/` — Network setup scripts

- **`docs/`** — Documentation
  - `README.md` — Quick start and overview
  - `REFERENCE.md` — Technical reference
  - `LOAD_TESTING.md` — How-to guides
  - Design documents (this file, etc.)

### Documentation Strategy (Diátaxis Approach)

To keep documentation scannable and accessible:

1. **Tutorials (Learning)** — `README.md`
   - Quick Start
   - Zero-config demo
   - First steps

2. **How-to Guides (Goal-oriented)** — `docs/LOAD_TESTING.md`, `docs/CLIENT_DEPLOYMENT.md`
   - Specific tasks
   - Step-by-step instructions
   - Common workflows

3. **Reference (Technical)** — `docs/REFERENCE.md`
   - Flag reference
   - API documentation
   - Configuration options
   - "Why Trust This Design"

4. **Explanation (Understanding)** — This document, `docs/DESIGN.md`
   - Architecture overview
   - How it works
   - Design decisions

### Accessibility & Formatting

**Guidelines**:

1. **Alt Text**: All diagrams and images include descriptive alt text for screen readers
2. **Plain English**: Acronyms explained on first use (e.g., "Reusable Configuration (DRY)")
3. **Callouts**: Use visual callouts for important information:
   ```markdown
   > **Tip**: Use `nix develop` to get all tools in one command.

   > **Warning**: Be respectful of public test streams. Use your own origin for load testing.

   > **Note**: Enhanced containers require Linux and Docker with specific capabilities.
   ```

4. **Scannable Tables**: Use tables for comparisons and quick reference
5. **Code Examples**: All code examples include comments and context

---

## Package Organization

### Complete Package List (Namespaced to Reduce Cognitive Load)

**Decision**: Use predictable prefixes or nested attributes to hide 20+ packages behind namespaces, reducing noise in `nix flake show`.

**Rationale**:
- **Less overwhelming**: `nix flake show` doesn't dump 20+ packages at top level
- **Predictable structure**: Users can discover packages via patterns
- **Power-user layer**: Packages are for advanced users; newcomers use apps

**Option 1: Nested Attributes (Preferred)**:
```nix
packages = {
  # Core packages (always available)
  ${meta.pname} = package;
  default = package;
  go-ffmpeg-hls-swarm-container = mainContainer;

  # Namespaced by component and profile
  test-origin = {
    default = {
      runner = testOriginProfiles.default.runner;
      container = testOriginProfiles.default.container;
    };
    low-latency = {
      runner = testOriginProfiles.low-latency.runner;
      container = testOriginProfiles.low-latency.container;
    };
    # ... other profiles
  };

  swarm-client = {
    default = {
      runner = swarmClientProfiles.default.runner;
      container = swarmClientProfiles.default.container;
    };
    stress = {
      runner = swarmClientProfiles.stress.runner;
    };
    # ... other profiles
  };
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  # Linux-only namespaced packages
  test-origin = {
    default = {
      container-enhanced = testOriginProfiles.default.containerEnhanced;
      vm = testOriginProfiles.default.microvm.vm;
    };
    low-latency = {
      vm = testOriginProfiles.low-latency.microvm.vm;
    };
    # ... other profiles
  };

  iso = testOriginProfiles.default.iso;
};
```

**Option 2: Predictable Prefixes (If nesting not desired)**:
```nix
packages = {
  # Core
  ${meta.pname} = package;
  default = package;
  go-ffmpeg-hls-swarm-container = mainContainer;

  # Predictable pattern: <component>-<type>-<profile>
  test-origin-runner-default = testOriginProfiles.default.runner;
  test-origin-runner-low-latency = testOriginProfiles.low-latency.runner;
  test-origin-container-default = testOriginProfiles.default.container;
  test-origin-container-low-latency = testOriginProfiles.low-latency.container;

  swarm-client-runner-default = swarmClientProfiles.default.runner;
  swarm-client-runner-stress = swarmClientProfiles.stress.runner;
  swarm-client-container-default = swarmClientProfiles.default.container;
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  test-origin-container-enhanced-default = testOriginProfiles.default.containerEnhanced;
  test-origin-vm-default = testOriginProfiles.default.microvm.vm;
  test-origin-vm-low-latency = testOriginProfiles.low-latency.microvm.vm;
  test-origin-iso = testOriginProfiles.default.iso;
};
```

**Usage Philosophy**:
- **Newcomers**: Use `apps` (e.g., `nix run .#up`, `nix run .#test-origin`)
- **Power users**: Use `packages` directly (e.g., `nix build .#test-origin.default.runner`)
- **Discovery**: `nix flake show` shows organized structure, not flat list of 20+ packages

---

## Usage Examples

### Development Shell

```bash
# Enter development shell (all platforms)
nix develop

# Available tools:
go version              # Go compiler
gopls version           # Go language server
golangci-lint --version # Linter
delve version           # Debugger
curl --version          # HTTP client
jq --version            # JSON processor
nil --version           # Nix language server
ffmpeg -version         # FFmpeg

# Build and test
go build ./cmd/go-ffmpeg-hls-swarm
go test ./...
golangci-lint run
```

### Main Binary Container

```bash
# Build (all platforms)
nix build .#go-ffmpeg-hls-swarm-container

# Load into Docker (Linux)
docker load < ./result

# Run with CLI arguments (traditional)
docker run --rm go-ffmpeg-hls-swarm:latest \
  -clients 10 -duration 30s \
  http://origin:8080/stream.m3u8

# Run with environment variables (Kubernetes/Nomad friendly)
docker run --rm \
  -e CLIENTS=100 \
  -e DURATION=60s \
  -e RAMP_RATE=5 \
  -e METRICS_PORT=9100 \
  -e LOG_LEVEL=info \
  go-ffmpeg-hls-swarm:latest \
  http://origin:8080/stream.m3u8

# Kubernetes example
kubectl run swarm-test --image=go-ffmpeg-hls-swarm:latest \
  --env="CLIENTS=100" \
  --env="DURATION=60s" \
  --env="STREAM_URL=http://origin:8080/stream.m3u8"
```

### Test Origin Deployment Options

#### Runner (All Platforms)

```bash
# Run locally (all platforms)
nix run .#test-origin
make test-origin
```

#### Basic Container (All Platforms Build, Linux Run)

```bash
# Build (all platforms)
nix build .#test-origin-container

# Load and run (Linux)
docker load < ./result
docker run --rm -p 8080:8080 go-ffmpeg-hls-swarm-test-origin:latest
```

#### Enhanced Container (Linux Only)

```bash
# Build (Linux only)
nix build .#test-origin-container-enhanced

# Load and run
docker load < ./result
docker run --rm -p 8080:8080 \
  --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
  --cap-add SYS_ADMIN \
  go-ffmpeg-hls-swarm-test-origin-enhanced:latest
```

#### MicroVM (Linux Only, Requires KVM)

```bash
# Build and run (Linux only, requires KVM)
nix run .#test-origin-vm
make microvm-origin
```

#### ISO Image (Linux Only)

```bash
# Build ISO (Linux only)
nix build .#test-origin-iso

# Build ISO with Cloud-Init support
nix build .#test-origin-iso --arg cloudInit '{
  enable = true;
  userData = builtins.readFile ./user-data.yml;
}'

# Result is a bootable ISO
ls -lh ./result/iso/*.iso

# Deploy to:
# - Proxmox: Upload ISO, create VM, boot from ISO
# - VMware: Create VM, attach ISO, boot
# - VirtualBox: Create VM, attach ISO, boot
# - QEMU: qemu-system-x86_64 -cdrom ./result/iso/*.iso -m 2048

# Cloud-Init user-data example (user-data.yml):
# #cloud-config
# ssh_authorized_keys:
#   - ssh-rsa AAAAB3NzaC1yc2E... user@host
# network:
#   version: 2
#   ethernets:
#     eth0:
#       dhcp4: true
```

### Unified CLI Entry Point

```bash
# Simple usage (defaults: profile=default, type=runner)
nix run .#up

# Specify profile and type
nix run .#up -- low-latency vm

# Help
nix run .#up -- --help

# Available types:
# - runner: Local shell script runner
# - container: Basic container
# - vm: MicroVM (Linux only)
```

### Nix Flake Check

```bash
# Run all checks (Go + Nix tests)
nix flake check

# Run only Nix test suite
nix run .#checks.nix-tests

# Run only shellcheck
nix run .#checks.shellcheck

# CI/CD integration
# GitHub Actions will automatically run `nix flake check`
```

### Cross-Platform CI/CD

**GitHub Actions Example**:

```yaml
# .github/workflows/nix-builds.yml
name: Nix Builds

on:
  push:
    branches: [main]
  pull_request:

jobs:
  build-universal:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - run: nix build .#go-ffmpeg-hls-swarm
      - run: nix build .#go-ffmpeg-hls-swarm-container
      - run: nix build .#test-origin-container

  build-linux-only:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - run: nix build .#test-origin-container-enhanced
      - run: nix build .#test-origin-iso
      - run: nix build .#test-origin-vm

  build-arm64:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Build ARM64 containers
        run: |
          nix build .#packages.aarch64-linux.go-ffmpeg-hls-swarm-container
          nix build .#packages.aarch64-linux.test-origin-container
```

### Remote Builders for ARM64

**For CI/CD with ARM64 support**, use remote builders:

**Option 1: Tailscale + Remote Builder**

```bash
# On Raspberry Pi or Graviton instance
# 1. Install Nix
curl --proto '=https' --tlsv1.2 -sSf https://get.determinate.systems/nix | sh -s -- install

# 2. Configure as remote builder
echo "builders = ssh-ng://builder@tailscale-ip aarch64-linux /etc/nix/machines" >> /etc/nix/nix.conf

# 3. In GitHub Actions or local CI
export NIX_REMOTE_BUILDERS="ssh-ng://builder@tailscale-ip aarch64-linux"
nix build .#packages.aarch64-linux.go-ffmpeg-hls-swarm-container
```

**Option 2: Determinate Systems Nix Installer + Remote Builder**

```yaml
# .github/workflows/arm64-builds.yml
jobs:
  build-arm64-remote:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: DeterminateSystems/nix-installer-action@main
      - name: Configure remote builder
        run: |
          echo "builders = ssh-ng://user@graviton-instance aarch64-linux /etc/nix/machines" >> ~/.config/nix/nix.conf
      - name: Build ARM64 packages
        run: |
          nix build .#packages.aarch64-linux.go-ffmpeg-hls-swarm-container
          nix build .#packages.aarch64-linux.test-origin-container
```

**Documentation**: Add to `docs/CI_CD.md`:
- How to set up remote builders
- Tailscale configuration
- Graviton/Raspberry Pi setup
- Troubleshooting remote builds

---

## Testing

### Testing Strategy Overview

The test suite in `scripts/nix-tests/` provides comprehensive, automated testing for all Nix builds. The strategy prioritizes:

1. **Fast checks first** — Profile accessibility before expensive builds
2. **Comprehensive coverage** — All packages, profiles, and deployment variants
3. **Platform awareness** — Skip unsupported tests gracefully
4. **Parallel execution** — Where possible, run tests in parallel
5. **Clear reporting** — Detailed summaries with pass/fail/skip counts

### Test Script Organization

```
scripts/nix-tests/
├── lib.sh                    # Shared utilities (enhanced)
├── shellcheck.sh             # Script validation
├── test-profiles.sh          # Profile accessibility (all platforms) - UPDATED
├── test-packages.sh          # Package builds (all platforms) - UPDATED
├── test-containers.sh        # Container builds + env var tests - UPDATED
├── test-microvms.sh          # MicroVM builds (Linux only) - UPDATED
├── test-iso.sh               # NEW: ISO builds (Linux only)
├── test-cli.sh               # NEW: Unified CLI entry point tests
├── test-apps.sh              # App execution (all platforms) - UPDATED
└── test-all.sh               # Run all tests - UPDATED
```

### Test Coverage Matrix

| Test Category | Platforms | Tests | Estimated Time |
|--------------|-----------|-------|----------------|
| **Profiles** | All | Profile accessibility (all profiles) | ~30s |
| **Packages** | All | Core package + all profile runners | ~5-10min |
| **Containers** | All (build), Linux (run) | All containers + env var support | ~10-15min |
| **MicroVMs** | Linux only | All MicroVM profile variants | ~15-20min |
| **ISO** | Linux only | ISO image build | ~10-15min |
| **Unified CLI** | All | `nix run .#up` with all combinations | ~2min |
| **Apps** | All | All app execution | ~1-2min |

**Total estimated time**: ~45-60 minutes (on Linux with KVM), ~20-30 minutes (on macOS)

### Enhanced Test Scripts

#### 1. `test-profiles.sh` — Profile Accessibility (UPDATED)

**Purpose**: Fast validation that all profiles are accessible and can be evaluated.

**Updates**:
- Test all test-origin profiles (including new ones from DRY generation)
- Test all swarm-client profiles
- Test main binary container
- Test enhanced container (Linux only)
- Test ISO (Linux only)
- Test all MicroVM variants (Linux only)

**Implementation**:
```bash
#!/usr/bin/env bash
# Test all profile accessibility

# Test-origin profiles (dynamically discovered or hardcoded list)
readonly TEST_ORIGIN_PROFILES=(
    "test-origin"
    "test-origin-low-latency"
    "test-origin-4k-abr"
    "test-origin-stress"
    "test-origin-logged"
    "test-origin-debug"
    "test-origin-tap"
    "test-origin-tap-logged"
)

# Swarm-client profiles
readonly SWARM_CLIENT_PROFILES=(
    "swarm-client"
    "swarm-client-stress"
    "swarm-client-gentle"
    "swarm-client-burst"
    "swarm-client-extreme"
)

# Universal packages
test_package_exists "go-ffmpeg-hls-swarm"
test_package_exists "go-ffmpeg-hls-swarm-container"
test_package_exists "test-origin-container"
test_package_exists "swarm-client-container"

# Test-origin profiles
for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
    test_package_exists "$profile"
done

# Swarm-client profiles
for profile in "${SWARM_CLIENT_PROFILES[@]}"; do
    test_package_exists "$profile"
done

# Linux-only packages
if is_linux; then
    test_package_exists "test-origin-container-enhanced"
    test_package_exists "test-origin-iso"

    # MicroVM profiles
    for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
        vm_name="test-origin-vm-${profile#test-origin-}"
        test_package_exists "$vm_name" || test_skip "$vm_name" "MicroVM not available"
    done
fi
```

#### 2. `test-packages.sh` — Package Builds (UPDATED)

**Purpose**: Verify all packages build successfully.

**Updates**:
- Test all profile combinations
- Test main binary container
- Test enhanced container (Linux only)
- Test ISO (Linux only)
- Parallel builds where possible (using `xargs -P`)

**Implementation**:
```bash
#!/usr/bin/env bash
# Test all package builds

# Core package
test_build "go-ffmpeg-hls-swarm"

# Main binary container
test_build "go-ffmpeg-hls-swarm-container"

# Test-origin profiles (all platforms)
for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
    test_build "$profile"
    test_build "${profile}-container"  # Basic container
done

# Swarm-client profiles
for profile in "${SWARM_CLIENT_PROFILES[@]}"; do
    test_build "$profile"
done

# Swarm-client container
test_build "swarm-client-container"

# Linux-only packages
if is_linux; then
    test_build "test-origin-container-enhanced"
    test_build "test-origin-iso"

    # MicroVM packages (if KVM available)
    if has_kvm; then
        for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
            vm_name="test-origin-vm-${profile#test-origin-}"
            test_build "$vm_name" || test_skip "$vm_name" "Build failed or not available"
        done
    fi
fi
```

#### 3. `test-containers.sh` — Container Builds + Validation (UPDATED)

**Purpose**: Test all container builds and validate environment variable support.

**Updates**:
- Test main binary container (NEW)
- Test all test-origin profile containers
- Test enhanced container (Linux only)
- Test swarm-client container
- Validate container images can be loaded
- Test environment variable support (Linux only, requires Docker/Podman)

**Implementation**:
```bash
#!/usr/bin/env bash
# Test container builds and validation

# Helper: Test container build and load
test_container_build() {
    local container_name=$1
    log_test "Building $container_name..."

    if nix build ".#packages.$SYSTEM.$container_name" --no-link 2>&1; then
        # Verify it's a valid container image
        local container_path
        container_path=$(nix build ".#packages.$SYSTEM.$container_name" --print-out-paths 2>&1)

        if [[ -n "$container_path" ]] && [[ -f "$container_path" ]]; then
            test_pass "$container_name (build)"

            # On Linux, test loading into Docker/Podman
            if is_linux && command -v docker >/dev/null 2>&1; then
                log_test "Loading $container_name into Docker..."
                if docker load < "$container_path" >/dev/null 2>&1; then
                    test_pass "$container_name (load)"
                else
                    test_fail "$container_name (load)" "Failed to load into Docker"
                fi
            fi
        else
            test_fail "$container_name" "Container path invalid"
        fi
    else
        test_fail "$container_name" "Build failed"
    fi
}

# Main binary container (NEW)
test_container_build "go-ffmpeg-hls-swarm-container"

# Test-origin containers (all profiles)
for profile in "${TEST_ORIGIN_PROFILES[@]}"; do
    test_container_build "${profile}-container"
done

# Enhanced container (Linux only)
if is_linux; then
    test_container_build "test-origin-container-enhanced"
fi

# Swarm-client container
test_container_build "swarm-client-container"
```

#### 4. `test-containers-env.sh` — Environment Variable Support (NEW)

**Purpose**: Test that containers correctly handle environment variables.

**Implementation**:
```bash
#!/usr/bin/env bash
# Test container environment variable support

if ! is_linux || ! command -v docker >/dev/null 2>&1; then
    log_warn "Skipping container env var tests (requires Linux + Docker)"
    exit 0
fi

# Test main binary container env vars
test_container_env() {
    local container_name=$1
    local image_name=$2

    log_test "Testing $container_name environment variables..."

    # Load container if not already loaded
    local container_path
    container_path=$(nix build ".#packages.$SYSTEM.$container_name" --print-out-paths 2>&1)
    docker load < "$container_path" >/dev/null 2>&1

    # Test env var mapping
    if docker run --rm "$image_name:latest" \
        -e CLIENTS=50 \
        -e DURATION=5s \
        --help >/dev/null 2>&1; then
        test_pass "$container_name (env vars)"
    else
        test_fail "$container_name (env vars)" "Env var support failed"
    fi
}

# Test main binary container
test_container_env "go-ffmpeg-hls-swarm-container" "go-ffmpeg-hls-swarm"

# Test swarm-client container (already has env var support)
test_container_env "swarm-client-container" "go-ffmpeg-hls-swarm"
```

#### 5. `test-microvms.sh` — MicroVM Builds (UPDATED)

**Purpose**: Test all MicroVM profile variants.

**Updates**:
- Test all profile combinations (default, low-latency, stress, logged, debug, tap, tap-logged)
- Verify MicroVM packages build
- Skip gracefully if KVM not available

**Implementation**:
```bash
#!/usr/bin/env bash
# Test MicroVM builds (Linux only, requires KVM)

if ! is_linux; then
    log_warn "Skipping MicroVM tests (not on Linux)"
    exit 0
fi

if ! has_kvm; then
    log_warn "Skipping MicroVM tests (KVM not available)"
    exit 0
fi

# Test all MicroVM profile variants
readonly MICROVM_PROFILES=(
    "test-origin-vm"
    "test-origin-vm-low-latency"
    "test-origin-vm-stress"
    "test-origin-vm-logged"
    "test-origin-vm-debug"
    "test-origin-vm-tap"
    "test-origin-vm-tap-logged"
)

for vm_profile in "${MICROVM_PROFILES[@]}"; do
    log_test "Building $vm_profile..."
    if nix build ".#packages.$SYSTEM.$vm_profile" --no-link 2>&1; then
        test_pass "$vm_profile"
    else
        test_fail "$vm_profile" "Build failed"
    fi
done
```

#### 6. `test-iso.sh` — ISO Image Builds (NEW)

**Purpose**: Test ISO image builds and optional Cloud-Init support.

**Implementation**:
```bash
#!/usr/bin/env bash
# Test ISO image builds (Linux only)

if ! is_linux; then
    log_warn "Skipping ISO tests (not on Linux)"
    exit 0
fi

log_info "Testing ISO image builds for $SYSTEM"
log_info "This may take 10-15 minutes..."
echo ""

# Test default ISO
log_test "Building test-origin-iso..."
if nix build ".#packages.$SYSTEM.test-origin-iso" --no-link 2>&1; then
    # Verify ISO file exists
    local iso_path
    iso_path=$(nix build ".#packages.$SYSTEM.test-origin-iso" --print-out-paths 2>&1)

    if find "$iso_path" -name "*.iso" | grep -q .; then
        test_pass "test-origin-iso"
    else
        test_fail "test-origin-iso" "ISO file not found"
    fi
else
    test_fail "test-origin-iso" "Build failed"
fi

# Test ISO with Cloud-Init (if supported)
# This would require passing --arg to nix build
# For now, just document that Cloud-Init is optional

print_summary
```

#### 7. `test-cli.sh` — Unified CLI Entry Point (NEW)

**Purpose**: Test the unified CLI entry point (`nix run .#up`).

**Implementation**:
```bash
#!/usr/bin/env bash
# Test unified CLI entry point

log_info "Testing unified CLI entry point"
echo ""

# Test default (should show help or run default)
log_test "Testing nix run .#up (default)..."
if nix run .#up -- --help >/dev/null 2>&1 || nix run .#up --help >/dev/null 2>&1; then
    test_pass "up (default)"
else
    test_fail "up (default)" "CLI not working"
fi

# Test profile selection
for profile in "default" "low-latency" "stress"; do
    log_test "Testing nix run .#up -- $profile runner..."
    if timeout 5 nix run .#up -- "$profile" "runner" >/dev/null 2>&1 || true; then
        test_pass "up ($profile runner)"
    else
        test_fail "up ($profile runner)" "Failed"
    fi
done

# Test type selection (Linux only for VM)
if is_linux && has_kvm; then
    log_test "Testing nix run .#up -- default vm..."
    if timeout 5 nix run .#up -- "default" "vm" >/dev/null 2>&1 || true; then
        test_pass "up (default vm)"
    else
        test_fail "up (default vm)" "Failed"
    fi
fi

print_summary
```

#### 8. `test-apps.sh` — App Execution (UPDATED)

**Purpose**: Test all Nix apps can be executed.

**Updates**:
- Test unified CLI app (`up`)
- Test all profile-specific apps
- Test MicroVM apps (Linux only)

**Implementation**:
```bash
#!/usr/bin/env bash
# Test app execution

# Core apps
readonly CORE_APPS=("welcome" "build" "run")

# Test-origin apps
readonly TEST_ORIGIN_APPS=(
    "test-origin"
    "test-origin-low-latency"
    "test-origin-4k-abr"
    "test-origin-stress"
    "test-origin-logged"
    "test-origin-debug"
)

# Unified CLI app (NEW)
test_app "up" "--help"

# Core apps
for app in "${CORE_APPS[@]}"; do
    test_app "$app"
done

# Test-origin apps
for app in "${TEST_ORIGIN_APPS[@]}"; do
    test_app "$app" "--help"  # Just test it runs, don't start server
done

# MicroVM apps (Linux only)
if is_linux && has_kvm; then
    test_app "test-origin-vm" "--help"
    test_app "test-origin-vm-logged" "--help"
fi
```

#### 9. `test-all.sh` — Orchestration (UPDATED)

**Purpose**: Run all test scripts and provide comprehensive summary.

**Updates**:
- Include new test scripts
- Better error handling
- Aggregate results across all scripts
- Performance timing

**Implementation**:
```bash
#!/usr/bin/env bash
# Run all Nix tests

START_TIME=$(date +%s)

log_info "════════════════════════════════════════════════════════════"
log_info "Running All Nix Tests"
log_info "════════════════════════════════════════════════════════════"
echo ""

# Fast checks first
"$SCRIPT_DIR/test-profiles.sh" || true
echo ""

# Package builds
"$SCRIPT_DIR/test-packages.sh" || true
echo ""

# Container builds
"$SCRIPT_DIR/test-containers.sh" || true
echo ""

# Container env var tests (Linux only)
if is_linux && command -v docker >/dev/null 2>&1; then
    "$SCRIPT_DIR/test-containers-env.sh" || true
    echo ""
fi

# Linux-only tests
if is_linux && has_kvm; then
    "$SCRIPT_DIR/test-microvms.sh" || true
    echo ""
fi

if is_linux; then
    "$SCRIPT_DIR/test-iso.sh" || true
    echo ""
fi

# Unified CLI tests
"$SCRIPT_DIR/test-cli.sh" || true
echo ""

# App execution
"$SCRIPT_DIR/test-apps.sh" || true
echo ""

# Final summary
END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

log_info "════════════════════════════════════════════════════════════"
log_info "All Tests Completed in ${DURATION}s"
log_info "════════════════════════════════════════════════════════════"
```

### Enhanced `lib.sh` Utilities

**New helper functions**:
```bash
# Test if a package builds successfully
test_build() {
    local package_name=$1
    log_test "Building $package_name..."
    if nix build ".#packages.$SYSTEM.$package_name" --no-link 2>&1; then
        test_pass "$package_name"
        return 0
    else
        test_fail "$package_name" "Build failed"
        return 1
    fi
}

# Test if an app can be executed
test_app() {
    local app_name=$1
    local app_args="${2:-}"
    log_test "Testing app: $app_name..."
    if timeout 5 nix run ".#$app_name" -- $app_args >/dev/null 2>&1 || true; then
        test_pass "$app_name"
    else
        test_fail "$app_name" "Execution failed"
    fi
}

# Check if Docker/Podman is available
has_docker() {
    command -v docker >/dev/null 2>&1 || command -v podman >/dev/null 2>&1
}
```

### Nix Flake Check Integration

**File**: `nix/checks.nix` (UPDATE)

```nix
# CI checks
{ pkgs, lib, meta, src, package }:

let
  # Nix test suite
  nixTests = pkgs.writeShellApplication {
    name = "nix-tests";
    runtimeInputs = [ pkgs.bash pkgs.nix pkgs.coreutils ];
    text = ''
      exec ${../scripts/nix-tests/test-all.sh}
    '';
  };

  # Shellcheck validation
  shellcheck = pkgs.writeShellApplication {
    name = "shellcheck";
    runtimeInputs = [ pkgs.shellcheck ];
    text = ''
      exec ${../scripts/nix-tests/shellcheck.sh}
    '';
  };
in
{
  # Existing Go checks
  format = meta.mkGoCheck {
    inherit src;
    name = "format";
    script = ''
      unformatted=$(gofmt -l .)
      [ -z "$unformatted" ] || { echo "Unformatted:"; echo "$unformatted"; exit 1; }
    '';
  };

  vet = meta.mkGoCheck {
    inherit src;
    name = "vet";
    script = "go vet ./...";
  };

  lint = meta.mkGoCheck {
    inherit src;
    name = "lint";
    script = "golangci-lint run ./...";
  };

  test = meta.mkGoCheck {
    inherit src;
    name = "test";
    script = "go test -v ./...";
  };

  build = package;

  # Nix test suite
  nix-tests = nixTests;

  # Shellcheck validation
  shellcheck = shellcheck;
}
```

**Usage**:
```bash
# Run all checks (Go + Nix tests)
nix flake check

# This validates:
# - Go code formatting
# - Go vet
# - Go linting
# - Go tests
# - All Nix package builds (all profiles)
# - All Nix container builds
# - All Nix app execution
# - Unified CLI entry point
# - Shell script validation (shellcheck)
```

### Performance Optimizations

1. **Parallel builds**: Use `xargs -P` for independent package builds
2. **Fast checks first**: Profile accessibility before expensive builds
3. **Caching**: Leverage Nix's build cache for repeated tests
4. **Skip gracefully**: Platform-specific tests skip on unsupported platforms
5. **Timeout protection**: Use `timeout` for app tests to prevent hangs

### Test Execution Strategy

**Local development**:
```bash
# Quick check (profiles only)
./scripts/nix-tests/test-profiles.sh

# Full test suite
./scripts/nix-tests/test-all.sh

# Specific category
./scripts/nix-tests/test-containers.sh
```

**CI/CD**:
```bash
# Via Nix flake check
nix flake check

# Or directly
./scripts/nix-tests/test-all.sh
```

### Manual Testing Checklist

1. **Main binary container**:
   ```bash
   nix build .#go-ffmpeg-hls-swarm-container
   docker load < ./result
   docker run --rm go-ffmpeg-hls-swarm:latest --help
   docker run --rm -e CLIENTS=10 go-ffmpeg-hls-swarm:latest --help
   ```

2. **Enhanced container**:
   ```bash
   nix build .#test-origin-container-enhanced
   docker load < ./result
   docker run --rm -p 8080:8080 --cap-add SYS_ADMIN \
     --tmpfs /tmp --tmpfs /run \
     go-ffmpeg-hls-swarm-test-origin-enhanced:latest
   curl http://localhost:8080/health
   ```

3. **ISO image**:
   ```bash
   nix build .#test-origin-iso
   # Test in QEMU
   qemu-system-x86_64 -cdrom ./result/iso/*.iso -m 2048
   ```

4. **Unified CLI**:
   ```bash
   nix run .#up -- --help
   nix run .#up -- low-latency runner
   nix run .#up -- default vm  # Linux only
   ```

---

## Final Polish

### Auto-Generated Shell Autocompletion

**Decision**: Generate shell completion scripts automatically from the single source of truth profile list.

**Rationale**:
- **Prevents typos**: Completion always matches available profiles
- **No manual maintenance**: Add profile to one list, completion updates automatically
- **Always in sync**: Same source as packages/apps

**Implementation**: See [Design Decision 13: Shell Autocompletion](#13-shell-autocompletion-auto-generated-from-single-source-of-truth) for full details.

**Usage**:
```bash
# Generate completion scripts
nix run .#generate-completion

# Install
source ./scripts/completion/bash-completion.sh  # Bash
source ./scripts/completion/zsh-completion.sh    # Zsh
```

### Container Healthcheck Metadata

**Decision**: Add OCI HealthCheck instructions to all containers that expose health endpoints.

**Rationale**:
- **Container orchestration**: Kubernetes, Docker Compose, Nomad can use healthchecks
- **Automatic recovery**: Orchestrators can restart unhealthy containers
- **Service discovery**: Health status visible in container metadata

**Implementation**:

**Main Binary Container** (`nix/container.nix`):
```nix
config = {
  # ... existing config ...

  # Healthcheck for metrics endpoint
  Healthcheck = {
    Test = [ "CMD" "curl" "-f" "http://localhost:9100/metrics" "||" "exit" "1" ];
    Interval = 30000000000;  # 30 seconds (nanoseconds)
    Timeout = 5000000000;    # 5 seconds
    StartPeriod = 10000000000;  # 10 seconds grace period
    Retries = 3;
  };
};
```

**Test Origin Containers** (`nix/test-origin/container.nix`, `nix/test-origin/container-enhanced.nix`):
```nix
config = {
  # ... existing config ...

  # Healthcheck for origin server /health endpoint
  Healthcheck = {
    Test = [ "CMD" "curl" "-f" "http://localhost:${toString config.server.port}/health" "||" "exit" "1" ];
    Interval = 30000000000;  # 30 seconds
    Timeout = 5000000000;    # 5 seconds
    StartPeriod = 30000000000;  # 30 seconds grace period (systemd needs time to start)
    Retries = 3;
  };
};
```

**Swarm Client Container** (`nix/swarm-client/container.nix`):
```nix
config = {
  # ... existing config ...

  # Healthcheck for metrics endpoint
  Healthcheck = {
    Test = [ "CMD" "curl" "-f" "http://localhost:${toString config.metricsPort}/metrics" "||" "exit" "1" ];
    Interval = 30000000000;  # 30 seconds
    Timeout = 5000000000;    # 5 seconds
    StartPeriod = 10000000000;  # 10 seconds grace period
    Retries = 3;
  };
};
```

**Benefits**:
1. **Kubernetes readiness**: `kubectl get pods` shows health status
2. **Docker Compose**: Automatic health monitoring
3. **Service mesh**: Istio, Linkerd can use healthchecks for routing
4. **Observability**: Health status visible in container metadata

**Verification**:
```bash
# Check healthcheck in built container
docker inspect go-ffmpeg-hls-swarm:latest | jq '.[0].Config.Healthcheck'

# Test healthcheck manually
docker run --rm go-ffmpeg-hls-swarm:latest &
sleep 5
docker ps  # Shows health status
```

---

## File Structure

```
nix/
├── lib.nix                    # Shared metadata and helpers
├── package.nix                # Go package build
├── shell.nix                  # Development shell
├── container.nix              # NEW: Main binary container
├── apps.nix                   # App definitions
├── checks.nix                 # Go checks
├── swarm-client/
│   ├── container.nix          # Swarm client container
│   └── ...
└── test-origin/
    ├── default.nix            # Entry point (updated)
    ├── config.nix             # Shared configuration
    ├── config/                # Configuration modules
    ├── ffmpeg.nix             # Shared FFmpeg script
    ├── nginx.nix              # Shared Nginx config
    ├── runner.nix             # Local runner (shell scripts)
    ├── container.nix          # Basic container (shell scripts)
    ├── container-enhanced.nix # NEW: Enhanced container (systemd)
    ├── nixos-module.nix       # Shared NixOS module (enhanced)
    ├── microvm.nix            # MicroVM wrapper (uses nixos-module)
    ├── iso.nix                # NEW: ISO image builder (uses nixos-module)
    └── sysctl.nix             # Kernel tuning
```

---

## Comparison of All Build Outputs

| Output | Platform | Isolation | Systemd | Config Source | Use Case |
|--------|----------|-----------|---------|---------------|----------|
| **Go package** | All | None | ❌ | N/A | Core binary |
| **Dev shell** | All | None | ❌ | N/A | Development |
| **Test origin runner** | All | None | ❌ | Shell scripts | Local testing |
| **Main binary container** | All (build), Linux (run) | Container | ❌ | CLI flags | Simple deployment |
| **Test origin basic container** | All (build), Linux (run) | Container | ❌ | Shell scripts | Simple origin |
| **Test origin enhanced container** | Linux only | Container | ✅ | NixOS module | Production-like |
| **Test origin MicroVM** | Linux only | Full VM | ✅ | NixOS module | High performance |
| **Test origin ISO** | Linux only | Full VM | ✅ | NixOS module | Traditional VM |
| **Swarm client container** | All (build), Linux (run) | Container | ❌ | Env vars | Client profiles |

---

## Security Considerations

1. **Non-root execution** — All containers run as non-root (default in Nix)
2. **Minimal contents** — Only required binaries included
3. **No secrets** — All configuration via CLI flags or environment variables
4. **TLS certificates** — Included for HTTPS stream support
5. **Systemd hardening** — Enhanced container, MicroVM, and ISO use hardened systemd services

---

## Future Enhancements

### Optional Enhancements

1. ✅ **Multi-arch containers** — ARM64 (aarch64-linux) support **IMPLEMENTED**
2. **Cloud images** — AWS AMI, GCP image, Azure VHD
3. **Kubernetes manifests** — Helm charts, K8s deployments
4. **Vagrant boxes** — Vagrant-compatible images
5. **Version tags** — Support versioned container tags
6. **MicroVM on ARM64** — Support aarch64-linux MicroVMs (requires MicroVM framework support)
7. **ISO on ARM64** — Support aarch64-linux ISO images

### Not in Scope

- Windows support (Unix-only tool)
- Cross-compilation (native builds only)
- Container registry publishing (separate concern)

---

## Questions for Review

1. **Enhanced container init**: Should we use `systemd-nspawn` or standard Docker with init system?
   - **Decision**: Standard Docker with init system (broader compatibility)

2. **ISO boot configuration**: Should ISO use DHCP or static IP by default?
   - **Decision**: DHCP by default, allow configuration via kernel parameters

3. **Container systemd**: Is full systemd in container worth the complexity?
   - **Decision**: Yes — enables exact MicroVM parity and better production testing

4. **Profile support**: Should all deployment variants support all profiles?
   - **Decision**: Yes — consistent behavior across all variants

5. **Main container entrypoint**: Should we support environment variables for common flags?
   - **Decision**: ✅ Yes — environment variables make containers "industry standard" for Kubernetes, Nomad, etc. Wrapper script maps env vars to CLI flags, with CLI args as fallback.

---

## Conclusion

This comprehensive design unifies all Nix builds into a coherent system that:

- ✅ Supports cross-platform builds (macOS, Linux, x86_64, aarch64)
- ✅ Provides complete development environment
- ✅ Offers multiple deployment options (runner, container, MicroVM, ISO)
- ✅ Shares configuration across variants (Reusable Configuration - DRY principle)
- ✅ Maintains consistency (same behavior everywhere)
- ✅ **Simplified user experience** — Unified CLI entry point (`nix run .#up`) with interactive fallback
- ✅ **Automated validation** — `nix flake check` runs all tests
- ✅ **Industry-standard containers** — Environment variable support for Kubernetes/Nomad
- ✅ **Multi-architecture support** — ARM64 (aarch64-linux) for cloud and edge
- ✅ **DRY package generation** — Functions generate packages from profile lists
- ✅ **Cloud-Init support** — Optional SSH keys and network config for ISO/MicroVM
- ✅ **Enhanced discoverability** — Interactive CLI, comprehensive OCI labels, shell completion
- ✅ **Accessibility** — Plain English, visual callouts, scannable tables, alt text for diagrams
- ✅ **Documentation strategy** — Diátaxis approach (Tutorials, How-to, Reference, Explanation)

### Key Innovations

1. **Reusable Configuration (DRY)**: Using `lib.genAttrs` to generate 20+ packages from profile lists instead of manual definitions
2. **Unified CLI with Interactive Fallback**: Single entry point (`nix run .#up`) with interactive menu when no args provided, reducing cognitive load for newcomers
3. **Nix Flake Check Integration**: One command (`nix flake check`) validates everything on user's architecture
4. **Environment Variable Support**: Containers work seamlessly with Kubernetes, Nomad, and other orchestrators
5. **Multi-Architecture**: ARM64 support enables deployment on Graviton, Raspberry Pi, and other ARM64 platforms
6. **Layered Images**: All containers use `buildLayeredImage` for optimal caching and download times
7. **Cloud-Init**: Optional support for injecting SSH keys and network configs without rebuilding
8. **Enhanced Container Metadata**: Comprehensive OCI labels for discoverability via `docker inspect`
9. **Docker Compose/Justfile**: One-command experience for enhanced containers, hiding complexity
10. **Shell Autocompletion**: Tab completion for profiles and deployment types
11. **Remote Builders**: CI/CD support for ARM64 via Tailscale or Determinate Systems
12. **Deployment Comparison Table**: Scannable table to help users choose the right deployment type

### User Experience Highlights

**For Newcomers**:
- 🚀 Zero-config demo at the top (immediate gratification)
- Interactive CLI menu (no need to remember profile names)
- One-command dev environment (`nix develop`)
- Clear "My Goal is..." table in README

**For Experienced Users**:
- Shell autocompletion
- Docker Compose/Justfile for enhanced containers
- Remote builders for ARM64
- Comprehensive test suite

**For All Users**:
- Scannable documentation (Diátaxis approach)
- Visual callouts for tips/warnings
- Plain English explanations
- Accessible formatting (alt text, tables)

The key innovation is reusing the NixOS module across enhanced container, MicroVM, and ISO, ensuring consistent behavior and reducing maintenance burden.

All deployment variants share the same configuration source, making it easy to:
- Test locally (runner)
- Deploy simply (container)
- Deploy with full isolation (MicroVM/ISO)

The design follows Nix idioms, maintains consistency with existing patterns, and prioritizes accessibility for a large audience while keeping the code clean and idiomatic. The focus on user experience, discoverability, and documentation ensures that both newcomers and experienced users can effectively use the system.
