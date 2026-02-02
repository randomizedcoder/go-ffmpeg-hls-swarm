# Nix Builds Comprehensive Implementation Plan

> **Type**: Implementation Plan
> **Status**: Draft - For Review
> **Related**: [NIX_BUILDS_COMPREHENSIVE_DESIGN.md](NIX_BUILDS_COMPREHENSIVE_DESIGN.md)

This document provides a detailed, step-by-step implementation plan for all Nix builds described in the comprehensive design document. Each phase includes specific file locations, function names, line numbers, definitions of done, and validation steps.

---

## Table of Contents

- [Overview](#overview)
- [High-Level Phase Summary](#high-level-phase-summary)
- [Safety Gates and Guardrails](#safety-gates-and-guardrails)
- [Build Dependency Map](#build-dependency-map)
- [Phase 0: Gatekeeper Script (Pre-Phase 1)](#phase-0-gatekeeper-script-pre-phase-1)
- [Phase 1: Single Source of Truth for Profiles](#phase-1-single-source-of-truth-for-profiles)
- [Phase 1.5: Dry Run Evaluation Testing](#phase-15-dry-run-evaluation-testing)
- [Phase 2: Main Binary Container](#phase-2-main-binary-container)
- [Phase 3: Enhanced Test-Origin Container](#phase-3-enhanced-test-origin-container)
- [Phase 4: ISO Image Builder](#phase-4-iso-image-builder)
- [Phase 5: Unified CLI Entry Point](#phase-5-unified-cli-entry-point)
- [Phase 6: Tiered Nix Flake Checks](#phase-6-tiered-nix-flake-checks)
- [Phase 7: Package Namespacing](#phase-7-package-namespacing)
- [Phase 8: Auto-Generated Shell Autocompletion](#phase-8-auto-generated-shell-autocompletion)
- [Phase 9: Docker Compose / Justfile Support](#phase-9-docker-compose--justfile-support)
- [Phase 10: Update Test Scripts](#phase-10-update-test-scripts)
- [Phase 11: Documentation Updates](#phase-11-documentation-updates)
- [Validation and Testing Strategy](#validation-and-testing-strategy)

---

## Overview

This implementation plan breaks down the comprehensive design into 11 phases, ordered by dependencies and logical grouping. Each phase is self-contained with clear inputs, outputs, and validation criteria.

### Implementation Order Rationale

1. **Contracts** (Interface Contracts) must be locked first - prevents contract mismatches
2. **Phase 0** (Gatekeeper) validates Phase 1 integrity
3. **Phase 1** (Single Source of Truth) must come first - all other phases depend on it
4. **Phase 1.5** (Dry Run Eval) catches logic errors before expensive builds
5. **Phases 2-4** (Containers/ISO) are independent and can be done in parallel
6. **Phase 7** (Namespacing) must complete before Phase 5 - namespacing decisions affect CLI resolution
7. **Phase 5** (Unified CLI) depends on Phase 1 and Phase 7 - can use stable names
8. **Phase 6** (Checks) depends on Phase 1 and existing test scripts
9. **Phase 8** (Autocompletion) depends on Phase 1 and Phase 5
10. **Phase 9** (Docker Compose) depends on Phase 3
11. **Phase 10** (Test Scripts) depends on all previous phases
12. **Phase 11** (Documentation) depends on all implementation phases

### Estimated Timeline

- **Contracts**: 1-2 hours (documentation and review)
- **Phase 0**: 1-2 hours
- **Phase 1**: 2-3 hours
- **Phase 1.5**: 1-2 hours
- **Phase 2**: 3-4 hours
- **Phase 3**: 4-5 hours
- **Phase 4**: 3-4 hours
- **Phase 7**: 3-4 hours (moved before Phase 5)
- **Phase 5**: 3-4 hours (reduced due to stable names from Phase 7)
- **Phase 6**: 2-3 hours
- **Phase 8**: 2-3 hours
- **Phase 9**: 1-2 hours
- **Phase 10**: 4-6 hours
- **Phase 11**: 2-3 hours

**Total Estimated Time**: 30-40 hours

### PR Slicing Strategy

To prevent mega-PR failures and enable safe review/rollback:

**Rule**: Each phase is its own PR

**Requirements per PR**:
- [ ] `nix flake show` remains clean (no broken derivations)
- [ ] `./scripts/nix-tests/test-eval.sh` passes
- [ ] Gatekeeper passes (if applicable)
- [ ] Phase-specific tests pass

**Compat Shim Policy**:
- One PR may include a "compat shim" (e.g., old names alias to new names)
- Compat shim must be removed in the very next PR
- Example: Phase 7 (namespacing) may alias old flat names to new nested names, but Phase 5 PR must remove aliases

**Benefits**:
- Safe review: Each PR is focused and testable
- Safe rollback: Can revert individual phases
- Clear progress: Each PR represents a milestone

---

## Safety Gates and Guardrails

To ensure smooth rollout and prevent skipping steps, the implementation includes automated safety gates:

1. **Gatekeeper Script** (Phase 0): Validates single source of truth integrity before proceeding
2. **Dry Run Evaluation** (Phase 1.5): Fast evaluation checks catch logic errors before expensive builds
3. **Evaluation Integrity Checks**: Every phase includes `nix flake show` validation to catch broken derivations
4. **Platform Parity Matrix**: Explicit tests ensure universal packages work on all platforms
5. **KVM Permission Checks**: Operational safety checks before attempting VM builds
6. **Atomic Commit Validation**: No phase merges unless evaluation checks pass

### Safety Gate Requirements

**Before any phase can be considered complete**:
- [ ] `nix flake show` produces no "broken derivation" warnings
- [ ] `nix eval .#packages.<system> --json` succeeds for all systems
- [ ] Gatekeeper script passes (if applicable)
- [ ] Platform-specific checks pass (KVM, Docker, etc.)

---

## Build Dependency Map

```
┌─────────────────────────────────────────────────────────────┐
│                    Phase Dependency Graph                    │
└─────────────────────────────────────────────────────────────┘

Contracts (Interface Contracts)
    │
    ▼
Phase 0 (Gatekeeper)
    │
    ▼
Phase 1 (Single Source of Truth) ◄───┐
    │                                  │
    ├──► Phase 1.5 (Dry Run Eval)     │
    │                                  │
    ├──► Phase 2 (Main Container)      │
    ├──► Phase 3 (Enhanced Container)  │
    ├──► Phase 4 (ISO Builder)        │
    ├──► Phase 7 (Namespacing) ◄───────┼─── Before Phase 5
    │                                  │
    ├──► Phase 5 (Unified CLI)        │
    ├──► Phase 6 (Tiered Checks)      │
    └──► Phase 8 (Autocompletion)      │
            │                          │
            └──────────────────────────┘
                    │
                    ▼
            Phase 9 (Docker Compose)
                    │
                    ▼
            Phase 10 (Test Scripts) ◄─── All previous phases
                    │
                    ▼
            Phase 11 (Documentation) ◄── All previous phases

Parallel Execution Opportunities:
- Phases 2, 3, 4 can run in parallel (after Phase 1.5)
- Phase 7 must complete before Phase 5 (namespacing affects CLI)
- Phases 6, 8 can run in parallel (after Phase 5)
```

---

## High-Level Phase Summary

| Phase | Name | Dependencies | Key Deliverables | Safety Gates |
|-------|------|--------------|------------------|--------------|
| 0 | Gatekeeper Script | None | `scripts/nix-tests/gatekeeper.sh` | Validates Phase 1 integrity |
| 1 | Single Source of Truth for Profiles | Phase 0 | Profile validation, single profile list | Gatekeeper validation |
| 1.5 | Dry Run Evaluation Testing | Phase 1 | Evaluation test suite | `nix eval` checks, no broken derivations |
| 2 | Main Binary Container | Phase 1.5 | `nix/container.nix`, env var support, healthcheck | Evaluation integrity |
| 3 | Enhanced Test-Origin Container | Phase 1.5 | `nix/test-origin/container-enhanced.nix`, systemd support | Evaluation integrity |
| 4 | ISO Image Builder | Phase 1.5 | `nix/test-origin/iso.nix`, Cloud-Init support | KVM check, evaluation integrity |
| 5 | Unified CLI Entry Point | Phase 1.5 | `nix/apps.nix` updates, contract-first dispatcher | Evaluation integrity |
| 6 | Tiered Nix Flake Checks | Phase 1.5, existing checks | `nix/checks.nix` updates, tiered structure | Evaluation integrity |
| 7 | Package Namespacing | Phase 1.5 | `flake.nix` package organization | Evaluation integrity |
| 8 | Auto-Generated Shell Autocompletion | Phase 1.5, Phase 5 | `nix/apps.nix` generate-completion app | Evaluation integrity |
| 9 | Docker Compose / Justfile | Phase 3 | `docker-compose.yaml`, `Justfile` | Evaluation integrity |
| 10 | Update Test Scripts | All previous phases | Updated test scripts, platform parity matrix | Platform parity tests |
| 11 | Documentation Updates | All previous phases | README, docs updates | Documentation accuracy checks |

---

## Interface Contracts (Pre-Phase 2 Lock)

### Overview

Before implementing Phase 2, all interface contracts must be locked. This prevents contract mismatches (naming, paths, app semantics) that only show up after multiple phases merge.

### Contract Checklist

**Must be completed and documented before Phase 2 begins.**

#### 1. Canonical Naming Scheme

**Package Naming Pattern**:
- Test Origin: `test-origin-<profile>-<type>`
  - Types: `runner`, `container`, `container-enhanced` (Linux only), `vm` (Linux only)
  - Examples: `test-origin-default-runner`, `test-origin-low-latency-container`, `test-origin-stress-vm`
- Swarm Client: `swarm-client-<profile>-<type>`
  - Types: `runner`, `container`
  - Examples: `swarm-client-default-runner`, `swarm-client-stress-container`
- Main Binary: `go-ffmpeg-hls-swarm-<type>`
  - Types: (none for binary), `container`
  - Examples: `go-ffmpeg-hls-swarm`, `go-ffmpeg-hls-swarm-container`

**App Naming Pattern**:
- Unified CLI: `up` (dispatcher)
- Generate completion: `generate-completion`
- Core apps: `run`, `build`, `welcome`

**Contract**: All packages and apps follow these patterns. No exceptions.

#### 2. Unified CLI (`.#up`) Contract

**Input Contract**:
- Accepts: `[profile] [type] [args...]`
- Profile: Must be from single source of truth (`profiles.nix`)
- Type: `runner`, `container`, `vm` (Linux only)
- Special: `--help` or `-h` shows help and exits
- Pass-through: All remaining args passed to underlying app

**Resolution Contract**:
- `profile` defaults to `"default"` if not provided
- `type` defaults to `"runner"` if not provided
- Resolution: `test-origin-<profile>-<type>` → `nix run .#test-origin-<profile>-<type>`
- Platform check: `vm` type fails with helpful error on non-Linux

**Output Contract**:
- Always prints dispatcher info before execution (transparency)
- Format: `"Executing: nix run .#<underlying> <args>"`
- Non-TTY mode: No prompts, uses defaults silently
- TTY mode: Interactive menu if no args provided

**Contract**: CLI must follow this exact behavior. No deviations.

#### 3. Gating Behavior Policy

**Policy**: **Omit attribute** (use `lib.optionalAttrs`)

**Rationale**: Cleaner `nix flake show`, no confusing error messages for unsupported platforms.

**Implementation**:
- Linux-only packages: `lib.optionalAttrs pkgs.stdenv.isLinux { ... }`
- KVM-only packages: `lib.optionalAttrs (pkgs.stdenv.isLinux && hasKVM) { ... }`
- Never: `package = if condition then value else throw "error"`

**Contract**: All platform-specific packages use `lib.optionalAttrs`. No `throw` for platform gating.

#### 4. Environment Variable Precedence Rule

**Policy**: **CLI args override env vars** (CLI takes precedence)

**Implementation Pattern** (all containers):
```bash
# Build args from env vars (if not overridden by CLI)
if ! echo "$*" | grep -qE '\s--clients\s'; then
    [ -n "${CLIENTS:-}" ] && ARGS+=(--clients "$CLIENTS")
fi
```

**Contract**: All containers follow this pattern. CLI always wins.

#### 5. Flake Output Structure Contract

**Top-Level Keys** (allowlist):
- `packages`: All buildable outputs
- `apps`: All runnable applications
- `checks`: All validation checks
- `devShells`: Development environments
- `formatter`: Code formatter

**No accidental outputs**: Only these keys should appear in `nix flake show`.

**Contract**: Gatekeeper script validates this structure.

### Contract Validation

Before Phase 2, verify:

- [ ] Naming scheme documented and agreed upon
- [ ] CLI contract documented with examples
- [ ] Gating policy chosen and documented
- [ ] Env var precedence rule documented
- [ ] Flake output structure documented
- [ ] All contracts reviewed and locked

### Contract Lock Document

Create `docs/NIX_INTERFACE_CONTRACTS.md` with all contracts above. This becomes the source of truth for all phases.

---

## Phase 0: Gatekeeper Script (Pre-Phase 1)

### Overview

Create a gatekeeper script that validates the single source of truth integrity. This script must pass before Phase 1 can be considered complete, and will be used to validate all subsequent phases.

### Files to Create

1. **`scripts/nix-tests/gatekeeper.sh`** (NEW)
   - Validates profile list integrity
   - Location: `scripts/nix-tests/gatekeeper.sh`

### Detailed Steps

#### Step 0.1: Create Gatekeeper Script

**File**: `scripts/nix-tests/gatekeeper.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Gatekeeper: Validates single source of truth integrity
# This script ensures profiles in profiles.nix appear in flake.nix
# Run before committing Phase 1 and after any profile changes

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

log_info "════════════════════════════════════════════════════════════"
log_info "Gatekeeper: Single Source of Truth Validation"
log_info "════════════════════════════════════════════════════════════"
echo ""

FAILED=0

# Test 1: Verify profile files exist
log_test "Checking profile definition files exist..."
if [[ ! -f "nix/test-origin/config/profiles.nix" ]]; then
    log_error "Missing: nix/test-origin/config/profiles.nix"
    FAILED=1
else
    test_pass "test-origin profiles.nix exists"
fi

if [[ ! -f "nix/swarm-client/config/profiles.nix" ]]; then
    log_error "Missing: nix/swarm-client/config/profiles.nix"
    FAILED=1
else
    test_pass "swarm-client profiles.nix exists"
fi

# Test 2: Verify profiles can be evaluated (via flake, not <nixpkgs>)
log_test "Evaluating profile lists..."
# Use flake's pkgs instead of <nixpkgs> to avoid surprises
if ! nix eval --impure --expr '
  let
    flake = builtins.getFlake (toString ./.);
    pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
    lib = pkgs.lib;
    to = import ./nix/test-origin/config/profiles.nix { inherit lib; };
    sc = import ./nix/swarm-client/config/profiles.nix { inherit lib; };
  in
  { test-origin = to.profiles; swarm-client = sc.profiles; }
' --json >/dev/null 2>&1; then
    log_error "Failed to evaluate profile lists"
    FAILED=1
else
    test_pass "Profile lists evaluate successfully"
fi

# Test 3: Verify flake.nix uses single source (if flake.nix exists and has been updated)
if [[ -f "flake.nix" ]]; then
    log_test "Checking flake.nix references single source..."

    # Check if flake.nix imports profiles.nix
    if ! grep -q "test-origin/config/profiles.nix" flake.nix; then
        log_warn "flake.nix may not be using single source of truth"
        log_warn "This is OK if Phase 1 is not yet complete"
    else
        test_pass "flake.nix references profiles.nix"
    fi
fi

# Test 4: Verify no broken derivations in flake show
log_test "Checking for broken derivations..."
if nix flake show 2>&1 | grep -qi "broken\|error"; then
    log_error "Found broken derivations in flake show"
    nix flake show 2>&1 | grep -i "broken\|error" || true
    FAILED=1
else
    test_pass "No broken derivations found"
fi

# Test 5: Verify evaluation integrity
log_test "Testing evaluation integrity..."
SYSTEM=$(nix eval --impure --expr 'builtins.currentSystem' --raw)
if ! nix eval ".#packages.$SYSTEM" --json >/dev/null 2>&1; then
    log_error "Failed to evaluate packages for $SYSTEM"
    FAILED=1
else
    test_pass "Packages evaluate successfully for $SYSTEM"
fi

# Test 6: Assert no accidental new outputs (allowlist check)
log_test "Checking flake output structure (allowlist)..."
FLAKE_OUTPUTS=$(nix flake show --json 2>/dev/null | jq -r 'keys[]' | sort)
EXPECTED_OUTPUTS="apps checks devShells formatter packages"
UNEXPECTED_OUTPUTS=$(comm -23 <(echo "$FLAKE_OUTPUTS" | tr '\n' ' ') <(echo "$EXPECTED_OUTPUTS" | tr ' ' '\n' | sort) | tr '\n' ' ')

if [[ -n "$UNEXPECTED_OUTPUTS" ]]; then
    log_error "Unexpected flake outputs found: $UNEXPECTED_OUTPUTS"
    log_error "Expected only: $EXPECTED_OUTPUTS"
    log_error "This may indicate accidentally exposed internals or renames"
    FAILED=1
else
    test_pass "Flake output structure matches allowlist"
fi

echo ""
if [[ $FAILED -eq 0 ]]; then
    log_info "✓ Gatekeeper: All checks passed"
    exit 0
else
    log_error "✗ Gatekeeper: Validation failed"
    exit 1
fi
```

**Make executable**:
```bash
chmod +x scripts/nix-tests/gatekeeper.sh
```

### Definition of Done

- [ ] `scripts/nix-tests/gatekeeper.sh` exists and is executable
- [ ] Script validates profile files exist
- [ ] Script validates profiles can be evaluated
- [ ] Script checks for broken derivations
- [ ] Script validates evaluation integrity
- [ ] Script provides clear error messages

### Validation Steps

1. **Run gatekeeper**:
   ```bash
   ./scripts/nix-tests/gatekeeper.sh
   ```

2. **Verify it fails appropriately** (before Phase 1):
   ```bash
   # Should fail if profiles.nix doesn't exist
   ./scripts/nix-tests/gatekeeper.sh
   ```

3. **Verify it passes** (after Phase 1):
   ```bash
   # Should pass after Phase 1 is complete
   ./scripts/nix-tests/gatekeeper.sh
   ```

---

## Phase 1: Single Source of Truth for Profiles

### Overview

Create a single source of truth for profile names with validation. All packages, apps, and scripts will derive from this list.

### Files to Create/Modify

1. **`nix/test-origin/config/profiles.nix`** (NEW)
   - Create file with profile list and validation function
   - Location: `nix/test-origin/config/profiles.nix`

2. **`nix/test-origin/config.nix`** (MODIFY)
   - Update to use profile validation
   - Location: Line ~9-23 (profile import section)

3. **`nix/swarm-client/config/profiles.nix`** (NEW)
   - Create file with swarm client profile list
   - Location: `nix/swarm-client/config/profiles.nix`

4. **`nix/swarm-client/config.nix`** (MODIFY)
   - Update to use profile validation
   - Location: Similar to test-origin

5. **`flake.nix`** (MODIFY)
   - Update to use single source of truth
   - Location: Lines ~138-160 (test origin and swarm client profile generation)

### Detailed Steps

#### Step 1.1: Create Test Origin Profile List

**File**: `nix/test-origin/config/profiles.nix` (NEW)

```nix
# Single source of truth for test-origin profile names
# All packages, apps, and scripts derive from this list
{ lib }:

let
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

  # Validation function: ensures profile exists
  validateProfile = profile:
    if lib.elem profile profiles then
      profile
    else
      throw "Unknown test-origin profile '${profile}'. Available: ${lib.concatStringsSep ", " profiles}";
in
{
  inherit profiles validateProfile;
}
```

**Validation**:
- File exists at correct path
- All current profiles are included
- Validation function throws clear error for invalid profiles

#### Step 1.2: Update Test Origin Config to Use Validation

**File**: `nix/test-origin/config.nix` (MODIFY)

**Location**: Lines ~9-23 (profile import section)

**Current Code** (approximate):
```nix
{ profile ? "default", overrides ? {}, lib, meta }:

let
  # Import split modules
  profiles = import ./config/profiles.nix;
  baseConfig = import ./config/base.nix;
```

**New Code**:
```nix
{ profile ? "default", overrides ? {}, lib, meta }:

let
  # Import profile list and validation
  profileConfig = import ./config/profiles.nix { inherit lib; };

  # Validate profile name
  validatedProfile = profileConfig.validateProfile profile;

  # Import split modules
  baseConfig = import ./config/base.nix;

  # Use generic profile system
  profileSystem = meta.mkProfileSystem {
    base = baseConfig;
    profiles = import ./config/profiles.nix { inherit lib; };
  };

  # Get merged config with validated profile
  mergedConfig = profileSystem.getConfig validatedProfile overrides;
```

**Validation**:
- Invalid profile names throw clear errors
- Valid profiles work as before
- All existing tests pass

#### Step 1.3: Create Swarm Client Profile List

**File**: `nix/swarm-client/config/profiles.nix` (NEW)

```nix
# Single source of truth for swarm-client profile names
{ lib }:

let
  profiles = [
    "default"
    "stress"
    "gentle"
    "burst"
    "extreme"
  ];

  validateProfile = profile:
    if lib.elem profile profiles then
      profile
    else
      throw "Unknown swarm-client profile '${profile}'. Available: ${lib.concatStringsSep ", " profiles}";
in
{
  inherit profiles validateProfile;
}
```

**Validation**: Same as Step 1.1

#### Step 1.4: Update Swarm Client Config

**File**: `nix/swarm-client/config.nix` (MODIFY)

**Location**: Similar pattern to test-origin

**Validation**: Same as Step 1.2

#### Step 1.5: Update Flake to Use Single Source

**File**: `flake.nix` (MODIFY)

**Location**: Lines ~138-160

**Current Code** (approximate):
```nix
testOriginDefault = import ./nix/test-origin { inherit pkgs lib meta microvm; };

testOriginProfiles = lib.mapAttrs
  (name: _: import ./nix/test-origin {
    inherit pkgs lib meta microvm;
    profile = name;
  })
  (lib.genAttrs testOriginDefault.availableProfiles (x: x));
```

**New Code**:
```nix
# Import single source of truth for profiles
testOriginProfileConfig = import ./nix/test-origin/config/profiles.nix { inherit (pkgs) lib; };
testOriginProfileNames = testOriginProfileConfig.profiles;

# Generate all profile variants (derives from single list)
testOriginProfiles = lib.genAttrs testOriginProfileNames (name:
  import ./nix/test-origin {
    inherit pkgs lib meta microvm;
    profile = testOriginProfileConfig.validateProfile name;
  }
);

# Same pattern for swarm-client
swarmClientProfileConfig = import ./nix/swarm-client/config/profiles.nix { inherit (pkgs) lib; };
swarmClientProfileNames = swarmClientProfileConfig.profiles;

swarmClientProfiles = lib.genAttrs swarmClientProfileNames (name:
  import ./nix/swarm-client {
    inherit pkgs lib meta;
    swarmBinary = package;
    profile = swarmClientProfileConfig.validateProfile name;
  }
);
```

**Validation**:
- All existing packages still build
- Profile validation works
- Invalid profile names fail with clear errors

### Definition of Done

- [ ] `nix/test-origin/config/profiles.nix` exists with all profiles and validation
- [ ] `nix/swarm-client/config/profiles.nix` exists with all profiles and validation
- [ ] Both config.nix files use profile validation
- [ ] `flake.nix` derives profiles from single source
- [ ] Invalid profile names throw clear errors
- [ ] All existing tests pass
- [ ] `nix eval .#packages` succeeds for all profiles
- [ ] **Gatekeeper script passes**: `./scripts/nix-tests/gatekeeper.sh` exits with code 0
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings

### Validation Steps

1. **Test profile validation**:
   ```bash
   # Should succeed
   nix eval .#packages.x86_64-linux.test-origin --raw

   # Should fail with clear error
   nix eval 'import ./nix/test-origin { profile = "invalid"; ... }' 2>&1 | grep -q "Unknown.*profile"
   ```

2. **Test single source of truth**:
   ```bash
   # Verify profiles match
   nix eval --impure --expr '
     let
       to = import ./nix/test-origin/config/profiles.nix { lib = (import <nixpkgs> {}).lib; };
       sc = import ./nix/swarm-client/config/profiles.nix { lib = (import <nixpkgs> {}).lib; };
     in
     { test-origin = to.profiles; swarm-client = sc.profiles; }
   ' --json
   ```

3. **Test existing functionality**:
   ```bash
   ./scripts/nix-tests/test-profiles.sh
   ```

4. **Run gatekeeper validation**:
   ```bash
   ./scripts/nix-tests/gatekeeper.sh
   # Must pass before proceeding to Phase 1.5
   ```

---

## Phase 1.5: Dry Run Evaluation Testing

### Overview

Fast evaluation testing to catch logic errors before expensive builds. This phase validates that all Nix expressions evaluate correctly without building anything.

### Rationale

- **Fast feedback**: `nix eval` takes seconds, `nix build` takes minutes
- **Early error detection**: Catches typos in `lib.genAttrs` logic before container builds
- **Prevents cascading failures**: Ensures Phase 1 implementation is correct before dependent phases

### Files to Create/Modify

1. **`scripts/nix-tests/test-eval.sh`** (NEW)
   - Evaluation-only tests
   - Location: `scripts/nix-tests/test-eval.sh`

2. **`nix/checks.nix`** (MODIFY - Early)
   - Add `nix-eval` check to quick tier
   - Location: Quick checks section

### Detailed Steps

#### Step 1.5.1: Create Evaluation Test Script

**File**: `scripts/nix-tests/test-eval.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Dry run evaluation tests - fast validation without builds
# Catches logic errors in lib.genAttrs, profile validation, etc.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "════════════════════════════════════════════════════════════"
log_info "Dry Run Evaluation Testing for $SYSTEM"
log_info "This should be fast (~10-30 seconds)..."
log_info "════════════════════════════════════════════════════════════"
echo ""

# Test 1: Evaluate all packages (no builds)
log_test "Evaluating all packages (no builds)..."
if nix eval ".#packages.$SYSTEM" --json >/dev/null 2>&1; then
    test_pass "All packages evaluate"
else
    test_fail "Package evaluation" "Failed to evaluate packages"
    exit 1
fi

# Test 2: Check for broken derivations
log_test "Checking for broken derivations..."
if nix flake show 2>&1 | grep -qiE "broken|error|failed"; then
    log_error "Found broken derivations:"
    nix flake show 2>&1 | grep -iE "broken|error|failed" || true
    test_fail "Broken derivations" "flake show reports errors"
    exit 1
else
    test_pass "No broken derivations"
fi

# Test 3: Verify profile validation works (via flake, not <nixpkgs>)
log_test "Testing profile validation..."
if nix eval --impure --expr '
  let
    flake = builtins.getFlake (toString ./.);
    pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
    lib = pkgs.lib;
    toConfig = import ./nix/test-origin/config/profiles.nix { inherit lib; };
    scConfig = import ./nix/swarm-client/config/profiles.nix { inherit lib; };
  in
  {
    test-origin-valid = toConfig.validateProfile "default";
    test-origin-invalid = tryEval (toConfig.validateProfile "invalid-profile");
    swarm-client-valid = scConfig.validateProfile "default";
    swarm-client-invalid = tryEval (scConfig.validateProfile "invalid-profile");
  }
' --json >/dev/null 2>&1; then
    test_pass "Profile validation evaluates"
else
    test_fail "Profile validation" "Failed to evaluate validation"
    exit 1
fi

# Test 4: Verify all profiles from single source are accessible (via flake)
log_test "Verifying all profiles are accessible..."
PROFILES=$(nix eval --impure --expr '
  let
    flake = builtins.getFlake (toString ./.);
    pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
    lib = pkgs.lib;
    toConfig = import ./nix/test-origin/config/profiles.nix { inherit lib; };
  in
  toConfig.profiles
' --json | jq -r '.[]')

for profile in $PROFILES; do
    if nix eval ".#packages.$SYSTEM.test-origin-$profile" --json >/dev/null 2>&1; then
        test_pass "test-origin-$profile (accessible)"
    else
        test_fail "test-origin-$profile" "Not accessible in packages"
    fi
done

# Test 5: Verify platform-specific packages are correctly gated
log_test "Verifying platform-specific package gating..."
if is_linux; then
    # Linux-only packages should exist
    if nix eval ".#packages.$SYSTEM.test-origin-container-enhanced" --json >/dev/null 2>&1; then
        test_pass "test-origin-container-enhanced (Linux, exists)"
    else
        test_fail "test-origin-container-enhanced" "Should exist on Linux"
    fi
else
    # Linux-only packages should not exist on non-Linux
    if nix eval ".#packages.$SYSTEM.test-origin-container-enhanced" --json >/dev/null 2>&1; then
        test_fail "test-origin-container-enhanced" "Should not exist on $(uname)"
    else
        test_pass "test-origin-container-enhanced (correctly omitted on $(uname))"
    fi
fi

# Test 6: Verify universal packages exist on all platforms
log_test "Verifying universal packages exist..."
UNIVERSAL_PACKAGES=(
    "go-ffmpeg-hls-swarm"
    "test-origin"
    "swarm-client"
)

for pkg in "${UNIVERSAL_PACKAGES[@]}"; do
    if nix eval ".#packages.$SYSTEM.$pkg" --json >/dev/null 2>&1; then
        test_pass "$pkg (universal, exists)"
    else
        test_fail "$pkg" "Universal package missing on $SYSTEM"
    fi
done

print_summary

# Exit with failure if any tests failed
if [[ $FAILED -gt 0 ]]; then
    log_error "Evaluation tests failed - do not proceed to Phase 2"
    exit 1
fi

log_info "✓ All evaluation tests passed - safe to proceed to Phase 2"
```

**Make executable**:
```bash
chmod +x scripts/nix-tests/test-eval.sh
```

#### Step 1.5.2: Add to Checks (Early)

**File**: `nix/checks.nix` (MODIFY - Early addition)

**Location**: Quick checks section (will be expanded in Phase 6)

**Add** (temporary, will be refined in Phase 6):
```nix
# Early addition for Phase 1.5 validation
nix-eval = pkgs.writeShellApplication {
  name = "nix-eval";
  runtimeInputs = [ pkgs.nix pkgs.jq ];
  text = ''
    exec ${../scripts/nix-tests/test-eval.sh}
  '';
};
```

### Definition of Done

- [ ] `scripts/nix-tests/test-eval.sh` exists and is executable
- [ ] All packages evaluate without errors
- [ ] No broken derivations in `nix flake show`
- [ ] Profile validation evaluates correctly
- [ ] All profiles from single source are accessible
- [ ] Platform-specific packages are correctly gated
- [ ] Universal packages exist on all platforms
- [ ] Script exits with code 0 (all tests pass)

### Validation Steps

1. **Run evaluation tests**:
   ```bash
   ./scripts/nix-tests/test-eval.sh
   # Must pass before proceeding to Phase 2
   ```

2. **Verify speed**:
   ```bash
   time ./scripts/nix-tests/test-eval.sh
   # Should complete in < 30 seconds
   ```

3. **Test on multiple systems** (if available):
   ```bash
   # On macOS
   ./scripts/nix-tests/test-eval.sh

   # On Linux
   ./scripts/nix-tests/test-eval.sh
   ```

### Safety Gate

**This phase must pass before proceeding to Phase 2**. If evaluation tests fail, fix Phase 1 before attempting container builds.

---

## Phase 2: Main Binary Container

### Overview

Create OCI container for the main `go-ffmpeg-hls-swarm` binary with environment variable support, healthcheck, and comprehensive metadata.

### Files to Create/Modify

1. **`nix/container.nix`** (NEW)
   - Complete container definition
   - Location: `nix/container.nix`

2. **`flake.nix`** (MODIFY)
   - Add container to packages
   - Location: Lines ~166-203 (packages section)

### Detailed Steps

#### Step 2.1: Create Main Binary Container

**File**: `nix/container.nix` (NEW)

**Complete Implementation**:
```nix
# OCI container image for go-ffmpeg-hls-swarm binary
# Supports environment variables for Kubernetes/Nomad orchestration
# Includes healthcheck for container orchestration
#
# Build: nix build .#go-ffmpeg-hls-swarm-container
# Load:  docker load < ./result
# Run:   docker run --rm go-ffmpeg-hls-swarm:latest -clients 10 http://origin:8080/stream.m3u8
#
{ pkgs, lib, package }:

let
  # Wrapper script with transparent environment variable support
  # Canonical mapping: CLI args override env vars (CLI takes precedence)
  entrypoint = pkgs.writeShellApplication {
    name = "swarm-entrypoint";
    runtimeInputs = [ package pkgs.ffmpeg-full ];
    text = ''
      set -euo pipefail

      # Canonical env var → CLI flag mapping
      # Rule: CLI args override env vars (CLI takes precedence)

      # Build args from env vars (if not overridden by CLI)
      ARGS=()

      # Only use env vars if corresponding CLI flag not present
      if ! echo "$*" | grep -qE '\s--clients\s'; then
        [ -n "''${CLIENTS:-}" ] && ARGS+=(--clients "$CLIENTS")
      fi

      if ! echo "$*" | grep -qE '\s--duration\s'; then
        [ -n "''${DURATION:-}" ] && ARGS+=(--duration "$DURATION")
      fi

      if ! echo "$*" | grep -qE '\s--ramp-rate\s'; then
        [ -n "''${RAMP_RATE:-}" ] && ARGS+=(--ramp-rate "$RAMP_RATE")
      fi

      if ! echo "$*" | grep -qE '\s--metrics-port\s'; then
        METRICS_PORT="''${METRICS_PORT:-9100}"
        ARGS+=(--metrics-port "$METRICS_PORT")
      fi

      if ! echo "$*" | grep -qE '\s--log-level\s'; then
        LOG_LEVEL="''${LOG_LEVEL:-info}"
        ARGS+=(--log-level "$LOG_LEVEL")
      fi

      # Debug mode: print resolved command
      if [[ "''${LOG_LEVEL:-}" == "debug" ]] || [[ "''${PRINT_CMD:-}" == "1" ]]; then
        echo "Resolved command: ${lib.getExe package} ''${ARGS[*]} $*" >&2
      fi

      # Execute (CLI args come after env-var-derived args, so CLI overrides)
      exec ${lib.getExe package} "''${ARGS[@]}" "$@"
    '';
  };
in
pkgs.dockerTools.buildLayeredImage {
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
      "org.opencontainers.image.description" = "HLS load testing with FFmpeg process orchestration";
      "org.opencontainers.image.source" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm";
      "org.opencontainers.image.documentation" = "https://github.com/randomizedcoder/go-ffmpeg-hls-swarm/blob/main/README.md";
      "org.opencontainers.image.version" = "0.1.0";
      "org.opencontainers.image.vendor" = "randomizedcoder";
      "org.opencontainers.image.licenses" = "MIT";
    };
  };

  fakeRootCommands = ''
    mkdir -p /tmp
    chmod 1777 /tmp
  '';

  maxLayers = 100;
}
```

**Key Components**:
- **Lines 12-64**: Entrypoint script with env var mapping
- **Lines 65-67**: `buildLayeredImage` call
- **Lines 69-80**: Container contents
- **Lines 82-120**: Container config (entrypoint, ports, env, healthcheck, labels)
- **Lines 122-127**: Fake root commands and layer optimization

#### Step 2.2: Integrate Container into Flake

**File**: `flake.nix` (MODIFY)

**Location**: Lines ~166-203 (packages section)

**Add after line ~168** (after `default = package;`):
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
```

### Definition of Done

- [ ] `nix/container.nix` exists with complete implementation
- [ ] Container builds on all platforms (`nix build .#go-ffmpeg-hls-swarm-container`)
- [ ] Container can be loaded into Docker (`docker load < ./result`)
- [ ] Environment variables work (`docker run -e CLIENTS=10 ...`)
- [ ] CLI args override env vars (verified per contract)
- [ ] Healthcheck is present (`docker inspect ... | jq '.[0].Config.Healthcheck'`)
- [ ] OCI labels are present and correct
- [ ] Container runs successfully
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings
- [ ] **Package evaluates**: `nix eval .#packages.<system>.go-ffmpeg-hls-swarm-container` succeeds
- [ ] **Smoke run passes** (Linux only): Container entrypoint works, help command succeeds
- [ ] **Metrics endpoint** (if applicable): `curl http://localhost:9100/metrics` returns 200

### Validation Steps

1. **Build container**:
   ```bash
   nix build .#go-ffmpeg-hls-swarm-container
   ```

2. **Load and inspect**:
   ```bash
   docker load < ./result
   docker inspect go-ffmpeg-hls-swarm:latest | jq '.[0].Config.Healthcheck'
   docker inspect go-ffmpeg-hls-swarm:latest | jq '.[0].Config.Labels'
   ```

3. **Test env vars**:
   ```bash
   docker run --rm -e CLIENTS=5 -e DURATION=10s go-ffmpeg-hls-swarm:latest --help
   ```

4. **Test CLI override**:
   ```bash
   docker run --rm -e CLIENTS=100 go-ffmpeg-hls-swarm:latest --clients 50 --help
   # Should use 50, not 100
   ```

5. **Test healthcheck** (if origin available):
   ```bash
   docker run -d --name test-swarm go-ffmpeg-hls-swarm:latest <args>
   sleep 15
   docker ps  # Should show health status
   ```

6. **Smoke run test** (Linux only, ensures entrypoint wiring works):
   ```bash
   # Build and load
   nix build .#go-ffmpeg-hls-swarm-container
   docker load < ./result

   # Test entrypoint works
   docker run --rm go-ffmpeg-hls-swarm:latest --help
   # Should show help text, not errors

   # Test metrics endpoint (if applicable)
   docker run -d --name test-swarm -p 9100:9100 go-ffmpeg-hls-swarm:latest <args>
   sleep 5
   curl -f http://localhost:9100/metrics
   # Should return 200 with metrics
   docker rm -f test-swarm
   ```

---

## Phase 3: Enhanced Test-Origin Container

### Overview

Create enhanced container with full NixOS systemd services, reusing the shared NixOS module. This provides MicroVM parity in a container.

### Files to Create/Modify

1. **`nix/test-origin/container-enhanced.nix`** (NEW)
   - Enhanced container with systemd
   - Location: `nix/test-origin/container-enhanced.nix`

2. **`nix/test-origin/default.nix`** (MODIFY)
   - Export `containerEnhanced`
   - Location: Return value section

3. **`flake.nix`** (MODIFY)
   - Add enhanced container to packages (Linux only)
   - Location: Linux-only packages section

### Detailed Steps

#### Step 3.1: Create Enhanced Container

**File**: `nix/test-origin/container-enhanced.nix` (NEW)

**Complete Implementation**:
```nix
# Enhanced OCI container with full NixOS systemd services
# Similar to MicroVM but runs in a container
# Uses the same nixos-module.nix for consistency
{ pkgs, lib, config, nixosModule, nixpkgs }:

let
  # Build minimal NixOS system with our module
  nixos = nixpkgs.lib.nixosSystem {
    system = "x86_64-linux";
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

in
pkgs.dockerTools.buildLayeredImage {
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

**Key Components**:
- **Lines 8-30**: NixOS system build with shared module
- **Lines 32-33**: Extract system closure
- **Lines 35-38**: `buildLayeredImage` with system closure
- **Lines 40-60**: Container config with healthcheck

#### Step 3.2: Export from Test-Origin Default

**File**: `nix/test-origin/default.nix` (MODIFY)

**Location**: Return value section (end of file)

**Add**:
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
}
```

#### Step 3.3: Add to Flake (Linux Only)

**File**: `flake.nix` (MODIFY)

**Location**: Linux-only packages section (after line ~1276 in current structure)

**Add**:
```nix
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  # Enhanced test origin container (requires systemd)
  test-origin-container-enhanced = testOriginProfiles.default.containerEnhanced or null;
```

### Definition of Done

- [ ] `nix/test-origin/container-enhanced.nix` exists
- [ ] Container builds on Linux (`nix build .#test-origin-container-enhanced`)
- [ ] Container uses shared NixOS module
- [ ] Systemd services start correctly
- [ ] Healthcheck is present and functional
- [ ] Container can be loaded and run with required Docker flags
- [ ] `/health` endpoint responds correctly
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings
- [ ] **Package evaluates**: `nix eval .#packages.<system>.test-origin-container-enhanced` succeeds (Linux only)
- [ ] **Smoke run passes**: Container entrypoint works, systemd starts, health endpoint responds
- [ ] **Metrics endpoints**: `/metrics`, `/health` return 200 after startup

### Validation Steps

1. **Build container**:
   ```bash
   nix build .#test-origin-container-enhanced
   ```

2. **Load and run**:
   ```bash
   docker load < ./result
   docker run --rm -p 8080:8080 \
     --cap-add SYS_ADMIN \
     --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
     go-ffmpeg-hls-swarm-test-origin-enhanced:latest
   ```

3. **Test health endpoint**:
   ```bash
   sleep 35  # Wait for systemd to start
   curl http://localhost:8080/health
   ```

4. **Verify healthcheck**:
   ```bash
   docker inspect go-ffmpeg-hls-swarm-test-origin-enhanced:latest | jq '.[0].Config.Healthcheck'
   ```

5. **Smoke run test** (ensures entrypoint and systemd work):
   ```bash
   # Build and load
   nix build .#test-origin-container-enhanced
   docker load < ./result

   # Test container starts (with required flags)
   docker run -d --name test-origin \
     --cap-add SYS_ADMIN \
     --tmpfs /tmp --tmpfs /run --tmpfs /run/lock \
     -p 8080:8080 -p 9100:9100 \
     go-ffmpeg-hls-swarm-test-origin-enhanced:latest

   # Wait for systemd to start services
   sleep 35

   # Test health endpoint
   curl -f http://localhost:8080/health
   # Should return 200

   # Test metrics endpoint
   curl -f http://localhost:9100/metrics
   # Should return 200 with metrics

   # Cleanup
   docker rm -f test-origin
   ```

---

## Phase 4: ISO Image Builder

### Overview

Create ISO image builder for bootable VM deployment, with optional Cloud-Init support.

### Files to Create/Modify

1. **`nix/test-origin/iso.nix`** (NEW)
   - ISO image builder
   - Location: `nix/test-origin/iso.nix`

2. **`nix/test-origin/default.nix`** (MODIFY)
   - Export `iso`
   - Location: Return value section

3. **`flake.nix`** (MODIFY)
   - Add ISO to packages (Linux only)
   - Location: Linux-only packages section

### Detailed Steps

#### Step 4.1: Create ISO Builder

**File**: `nix/test-origin/iso.nix` (NEW)

**Complete Implementation**:
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

in
iso.config.system.build.isoImage
```

**Key Components**:
- **Lines 10-12**: System definition
- **Lines 14-16**: ISO base module + shared NixOS module
- **Lines 18-60**: ISO-specific configuration
- **Lines 44-56**: Optional Cloud-Init support
- **Lines 63-64**: Return ISO image

#### Step 4.2: Export from Test-Origin Default

**File**: `nix/test-origin/default.nix` (MODIFY)

**Location**: Return value section

**Add**:
```nix
  # ISO image (Linux only)
  iso = if pkgs.stdenv.isLinux then
    import ./iso.nix {
      inherit pkgs lib config nixosModule;
      nixpkgs = pkgs;
      cloudInit = null;  # Can be overridden via flake args
    }
  else
    null;
```

#### Step 4.3: Add to Flake (Linux Only)

**File**: `flake.nix` (MODIFY)

**Location**: Linux-only packages section

**Add**:
```nix
  # ISO image (requires NixOS)
  test-origin-iso = testOriginProfiles.default.iso or null;
```

### Definition of Done

- [ ] `nix/test-origin/iso.nix` exists
- [ ] ISO builds on Linux (`nix build .#test-origin-iso`)
- [ ] ISO file exists in result (`find ./result -name "*.iso"`)
- [ ] ISO can be booted in QEMU/VirtualBox
- [ ] HLS origin service starts on boot
- [ ] Cloud-Init support is optional (doesn't break without it)
- [ ] **KVM check**: Test scripts verify `/dev/kvm` permissions before building
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings
- [ ] **Package evaluates**: `nix eval .#packages.<system>.test-origin-iso` succeeds (Linux only)

### Validation Steps

1. **Build ISO**:
   ```bash
   nix build .#test-origin-iso
   ```

2. **Verify ISO exists**:
   ```bash
   find ./result -name "*.iso"
   ls -lh ./result/iso/*.iso
   ```

3. **Test in QEMU** (optional):
   ```bash
   qemu-system-x86_64 -cdrom ./result/iso/*.iso -m 2048
   ```

4. **Verify services** (after boot):
   ```bash
   curl http://<iso-ip>:8080/health
   ```

---

## Phase 5: Unified CLI Entry Point

### Overview

Create unified CLI entry point (`nix run .#up`) with dispatcher pattern, TTY-aware interactive mode, and comprehensive help.

### Files to Create/Modify

1. **`nix/apps.nix`** (MODIFY)
   - Add `up` app with dispatcher pattern
   - Location: After existing apps

2. **`flake.nix`** (MODIFY)
   - Add `up` to apps
   - Location: Apps section

### Detailed Steps

#### Step 5.1: Create Unified CLI App

**File**: `nix/apps.nix` (MODIFY)

**Location**: After line ~26 (after `run` app)

**Add**:
```nix
  # Unified CLI entry point (dispatcher pattern)
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

      # Platform check for VM
      if [[ "$TYPE" == "vm" ]] && [[ "$(uname)" != "Linux" ]]; then
        echo "Error: VM deployment requires Linux with KVM support."
        echo ""
        echo "You're on $(uname). Try one of these instead:"
        echo "  • Runner: nix run .#up -- $PROFILE runner"
        echo "  • Container: nix run .#up -- $PROFILE container"
        exit 1
      fi

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

**Key Components**:
- **Lines 8-35**: Help text
- **Lines 37-42**: TTY detection
- **Lines 44-47**: Non-TTY defaults
- **Lines 48-80**: Interactive menu (gum or bash select)
- **Lines 81-85**: Argument parsing
- **Lines 87-99**: **Contract-first dispatcher** - queries Nix flake for available apps
- **Lines 101-109**: Platform check for VM
- **Lines 111-120**: Dispatcher output
- **Lines 122-123**: Execute

**Contract-First Dispatcher Pattern** (Lines 87-99):
Instead of hardcoding profile names, the dispatcher queries the Nix flake:

```bash
# Query available apps from flake (contract-first)
AVAILABLE_APPS=$(nix eval --json 'builtins.attrNames (builtins.getFlake "file://$PWD").apps.x86_64-linux' | jq -r '.[]')

# Filter test-origin apps
TEST_ORIGIN_APPS=$(echo "$AVAILABLE_APPS" | grep "^test-origin")

# Use available apps instead of hardcoded list
# This ensures new profiles are automatically discovered
```

**Alternative (Simpler)**: Use single source of truth directly:
```bash
# Get profiles from single source
PROFILES=$(nix eval --impure --expr '
  import ./nix/test-origin/config/profiles.nix { lib = (import <nixpkgs> {}).lib; }
' --json | jq -r '.profiles[]')

# Use in interactive menu
for profile in $PROFILES; do
  # Add to menu
done
```

#### Step 5.2: Add to Flake Apps

**File**: `flake.nix` (MODIFY)

**Location**: Apps section (after line ~209)

**Add**:
```nix
        apps = appsBase // {
          # Unified CLI entry point
          up = appsBase.up;

          # ... existing apps ...
```

### Definition of Done

- [ ] `up` app exists in `nix/apps.nix`
- [ ] `nix run .#up -- --help` shows comprehensive help
- [ ] `nix run .#up` (no args, TTY) shows interactive menu
- [ ] `nix run .#up` (no args, non-TTY) uses defaults
- [ ] `nix run .#up -- default runner` works
- [ ] Dispatcher prints what it will do before execution
- [ ] Platform checks work (VM on non-Linux shows helpful error)
- [ ] All profile/type combinations work
- [ ] **Contract-first**: Dispatcher queries Nix flake or single source for profiles (not hardcoded)
- [ ] **New profiles auto-discovered**: Adding profile to profiles.nix makes it available in CLI without code changes
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings

### Validation Steps

1. **Test help**:
   ```bash
   nix run .#up -- --help
   ```

2. **Test interactive (TTY)**:
   ```bash
   nix run .#up
   # Should show menu
   ```

3. **Test non-interactive (non-TTY)**:
   ```bash
   echo "" | nix run .#up
   # Should use defaults
   ```

4. **Test dispatcher output**:
   ```bash
   nix run .#up -- default runner --help
   # Should print dispatcher info before executing
   ```

5. **Test platform check**:
   ```bash
   # On macOS, should show helpful error
   nix run .#up -- default vm
   ```

---

## Phase 6: Tiered Nix Flake Checks

### Overview

Implement tiered checks system (quick/build/full) to avoid "building the world" surprises.

### Files to Create/Modify

1. **`nix/checks.nix`** (MODIFY)
   - Add tiered structure
   - Location: Entire file

### Detailed Steps

#### Step 6.1: Update Checks with Tiers

**File**: `nix/checks.nix` (MODIFY)

**Replace entire file**:
```nix
# CI checks - Tiered structure
# Default: quick checks (fast, local-friendly)
# Explicit tiers: quick, build, full
{ pkgs, lib, meta, src, package }:

let
  # Quick checks: fast validation (fmt/vet/lint/unit tests + cheap Nix eval)
  quick = {
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
      text = "exec ${../scripts/nix-tests/shellcheck.sh}";
    };
  };

  # Build checks: build key packages/containers (default profile only)
  build = quick // {
    build-core = package;  # Core Go binary

    build-default-runner = import ../flake.nix {
      # Access testOriginProfiles.default.runner
      # This is a simplified example - actual implementation may vary
    };

    build-main-container = import ./container.nix {
      inherit pkgs lib;
      package = package;
    };
  };

  # Full checks: build all profiles/variants (CI-only / opt-in)
  full = build // {
    # All profile builds (via test scripts)
    nix-tests = pkgs.writeShellApplication {
      name = "nix-tests";
      runtimeInputs = [ pkgs.bash pkgs.nix ];
      text = ''
        exec ${../scripts/nix-tests/test-all.sh}
      '';
    };
  };
in
{
  # Default: quick checks (fast, ~30 seconds)
  default = quick;

  # Explicit tiers
  quick = quick;
  build = build;
  full = full;
}
```

**Note**: The `build` tier implementation may need adjustment based on how to access test-origin profiles from checks. This is a simplified example.

### Definition of Done

- [ ] `nix/checks.nix` has tiered structure
- [ ] `nix flake check` runs quick checks by default
- [ ] `nix flake check .#checks.quick` works
- [ ] `nix flake check .#checks.build` builds key packages
- [ ] `nix flake check .#checks.full` runs full test suite
- [ ] CI can use different tiers

### Validation Steps

1. **Test default (quick)**:
   ```bash
   nix flake check
   # Should be fast (~30 seconds)
   ```

2. **Test explicit tiers**:
   ```bash
   nix flake check .#checks.quick
   nix flake check .#checks.build
   nix flake check .#checks.full
   ```

3. **Verify timing**:
   ```bash
   time nix flake check .#checks.quick  # Should be < 1 minute
   ```

---

## Phase 7: Package Namespacing

### Overview

Organize packages using nested attributes or predictable prefixes to reduce cognitive load in `nix flake show`.

### Files to Create/Modify

1. **`flake.nix`** (MODIFY)
   - Reorganize packages with namespacing
   - Location: Packages section (lines ~166-203)

### Detailed Steps

#### Step 7.1: Implement Namespacing

**File**: `flake.nix` (MODIFY)

**Location**: Packages section

**Option A: Nested Attributes (Preferred)**

Replace packages section with:
```nix
packages = {
  # Core packages (always available)
  ${meta.pname} = package;
  default = package;
  go-ffmpeg-hls-swarm-container = import ./nix/container.nix {
    inherit pkgs lib;
    package = package;
  };

  # Namespaced by component and profile
  test-origin = lib.genAttrs testOriginProfileNames (name: {
    runner = testOriginProfiles.${name}.runner;
    container = testOriginProfiles.${name}.container;
  });

  swarm-client = lib.genAttrs swarmClientProfileNames (name: {
    runner = swarmClientProfiles.${name}.runner;
    container = if name == "default" then swarmClientProfiles.${name}.container else null;
  });
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  # Linux-only namespaced packages
  test-origin = (lib.genAttrs testOriginProfileNames (name: {
    container-enhanced = if name == "default" then testOriginProfiles.${name}.containerEnhanced else null;
    vm = testOriginProfiles.${name}.microvm.vm or null;
  })) // {
    iso = testOriginProfiles.default.iso or null;
  };
};
```

**Option B: Predictable Prefixes (If nesting not desired)**

Use pattern: `<component>-<type>-<profile>`

### Definition of Done

**Technical**:
- [ ] Packages are organized with namespacing
- [ ] `nix flake show` shows organized structure
- [ ] All packages still accessible
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings

**User-Facing Behavior** (Critical UX checks):
- [ ] **Small, unsurprising surface area**: `nix flake show` has clean structure
- [ ] **Apps are primary UX**: Newcomers discover via `apps` (e.g., `nix run .#up`)
- [ ] **Packages are power-user**: Advanced users can access via `packages` (e.g., `nix build .#test-origin-default-runner`)
- [ ] **No accidental exposure**: Only expected top-level keys (apps, packages, checks, devShells, formatter)

### Validation Steps

1. **Verify structure**:
   ```bash
   nix flake show
   # Should show organized structure, not flat list
   ```

2. **Verify packages still work**:
   ```bash
   nix build .#test-origin.default.runner
   # Or with prefixes: nix build .#test-origin-runner-default
   ```

---

## Phase 8: Auto-Generated Shell Autocompletion

### Overview

Create app that generates shell completion scripts from single source of truth.

### Files to Create/Modify

1. **`nix/apps.nix`** (MODIFY)
   - Add `generate-completion` app
   - Location: After `up` app

2. **`scripts/completion/`** (NEW DIRECTORY)
   - Directory for generated completion scripts

### Detailed Steps

#### Step 8.1: Create Generate-Completion App

**File**: `nix/apps.nix` (MODIFY)

**Location**: After `up` app

**Add**:
```nix
  # Auto-generate shell completion from single source of truth
  generate-completion = mkApp (pkgs.writeShellApplication {
    name = "generate-completion";
    runtimeInputs = [ pkgs.bash pkgs.nix ];
    text = ''
      set -euo pipefail

      # Extract profiles from single source of truth (via flake, not <nixpkgs>)
      PROFILES=$(nix eval --impure --expr '
        let
          flake = builtins.getFlake (toString ./.);
          pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
          lib = pkgs.lib;
          profileConfig = import ./nix/test-origin/config/profiles.nix { inherit lib; };
        in
        lib.concatStringsSep " " profileConfig.profiles
      ' --raw)

      TYPES="runner container vm"
      OUTPUT_DIR="''${1:-./scripts/completion}"

      mkdir -p "$OUTPUT_DIR"

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

#### Step 8.2: Add to Flake Apps

**File**: `flake.nix` (MODIFY)

**Location**: Apps section

**Add**:
```nix
          generate-completion = appsBase.generate-completion;
```

### Definition of Done

- [ ] `generate-completion` app exists
- [ ] `nix run .#generate-completion` generates scripts
- [ ] Generated scripts work with bash and zsh
- [ ] Profiles match single source of truth
- [ ] Completion prevents typos

### Validation Steps

1. **Generate completion**:
   ```bash
   nix run .#generate-completion
   ```

2. **Test bash completion**:
   ```bash
   source ./scripts/completion/bash-completion.sh
   nix run .#up <TAB><TAB>  # Should show profiles
   ```

3. **Test zsh completion**:
   ```bash
   source ./scripts/completion/zsh-completion.sh
   nix run .#up <TAB><TAB>  # Should show profiles
   ```

---

## Phase 9: Docker Compose / Justfile Support

### Overview

Create Docker Compose and Justfile for simplified enhanced container usage.

### Files to Create/Modify

1. **`docker-compose.yaml`** (NEW)
   - Docker Compose configuration
   - Location: Repository root

2. **`Justfile`** (NEW)
   - Justfile with recipes
   - Location: Repository root

### Detailed Steps

#### Step 9.1: Create Docker Compose

**File**: `docker-compose.yaml` (NEW)

**Complete Implementation**:
```yaml
version: '3.8'

services:
  test-origin-enhanced:
    image: go-ffmpeg-hls-swarm-test-origin-enhanced:latest
    build:
      context: .
      dockerfile: Dockerfile.enhanced  # Or use: nix build .#test-origin-container-enhanced
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
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:8080/health"]
      interval: 30s
      timeout: 5s
      start_period: 30s
      retries: 3
```

#### Step 9.2: Create Justfile

**File**: `Justfile` (NEW)

**Complete Implementation**:
```just
# Justfile for go-ffmpeg-hls-swarm
# Install: https://github.com/casey/just

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

# Main binary container
main-container:
    #!/usr/bin/env bash
    set -euo pipefail
    nix build .#go-ffmpeg-hls-swarm-container
    docker load < ./result
    echo "Container loaded: go-ffmpeg-hls-swarm:latest"
```

### Definition of Done

- [ ] `docker-compose.yaml` exists
- [ ] `Justfile` exists
- [ ] `docker-compose up` works (after building container)
- [ ] `just enhanced-origin` works
- [ ] Required Docker flags are hidden from users

### Validation Steps

1. **Test Docker Compose**:
   ```bash
   nix build .#test-origin-container-enhanced
   docker load < ./result
   docker-compose up
   ```

2. **Test Justfile**:
   ```bash
   just enhanced-origin
   ```

---

## Phase 10: Update Test Scripts

### Overview

Update all test scripts to cover new packages, containers, and features.

### Files to Create/Modify

1. **`scripts/nix-tests/test-containers.sh`** (MODIFY)
   - Add main binary container and enhanced container tests
   - Location: Various sections

2. **`scripts/nix-tests/test-containers-env.sh`** (NEW)
   - Test environment variable support
   - Location: `scripts/nix-tests/test-containers-env.sh`

3. **`scripts/nix-tests/test-iso.sh`** (NEW)
   - Test ISO builds
   - Location: `scripts/nix-tests/test-iso.sh`

4. **`scripts/nix-tests/test-cli.sh`** (NEW)
   - Test unified CLI
   - Location: `scripts/nix-tests/test-cli.sh`

5. **`scripts/nix-tests/test-packages.sh`** (MODIFY)
   - Update for namespaced packages
   - Location: Package list sections

6. **`scripts/nix-tests/test-profiles.sh`** (MODIFY)
   - Update for single source of truth
   - Location: Profile list sections

7. **`scripts/nix-tests/test-apps.sh`** (MODIFY)
   - Add `up` and `generate-completion` apps
   - Location: App list sections

8. **`scripts/nix-tests/test-all.sh`** (MODIFY)
   - Include new test scripts
   - Location: Test execution section

9. **`scripts/nix-tests/lib.sh`** (MODIFY)
   - Add new helper functions
   - Location: Function definitions

### Detailed Steps

#### Step 10.1: Update lib.sh with Helpers

**File**: `scripts/nix-tests/lib.sh` (MODIFY)

**Location**: After existing functions (after line ~95)

**Add**:
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

# Check KVM permissions (operational safety)
check_kvm_permissions() {
    if ! is_linux; then
        return 1
    fi

    if [[ ! -e /dev/kvm ]]; then
        log_warn "KVM device not found: /dev/kvm"
        return 1
    fi

    if [[ ! -r /dev/kvm ]] || [[ ! -w /dev/kvm ]]; then
        log_warn "KVM device exists but lacks read/write permissions"
        log_warn "Run: sudo chmod 666 /dev/kvm (or add user to kvm group)"
        return 1
    fi

    return 0
}

# Test platform parity (universal packages must exist on all platforms)
test_platform_parity() {
    local package_name=$1
    local expected_platforms="${2:-all}"

    log_test "Testing platform parity for $package_name..."

    # Test on current system
    if ! nix eval ".#packages.$SYSTEM.$package_name" --json >/dev/null 2>&1; then
        test_fail "$package_name (platform parity)" "Missing on $SYSTEM"
        return 1
    fi

    # If marked as universal, should exist on all supported systems
    if [[ "$expected_platforms" == "all" ]]; then
        # Try to evaluate on other systems (may fail if cross-compilation not set up, that's OK)
        for other_system in x86_64-linux aarch64-linux x86_64-darwin aarch64-darwin; do
            if [[ "$other_system" != "$SYSTEM" ]]; then
                if nix eval ".#packages.$other_system.$package_name" --json >/dev/null 2>&1; then
                    test_pass "$package_name (exists on $other_system)"
                else
                    # Not a failure if cross-compilation not configured, but log it
                    log_warn "$package_name not evaluable on $other_system (may need cross-compilation setup)"
                fi
            fi
        done
    fi

    test_pass "$package_name (platform parity)"
    return 0
}
```

#### Step 10.2: Update test-containers.sh

**File**: `scripts/nix-tests/test-containers.sh` (MODIFY)

**Location**: After line ~18 (after log_info)

**Add main binary container test**:
```bash
# Main binary container (NEW)
log_test "Building go-ffmpeg-hls-swarm-container..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --no-link 2>&1; then
    # Verify it's a valid container image
    local container_path
    container_path=$(nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm-container" --print-out-paths 2>&1)

    if [[ -n "$container_path" ]] && [[ -f "$container_path" ]]; then
        test_pass "go-ffmpeg-hls-swarm-container (build)"

        # On Linux, test loading into Docker/Podman
        if is_linux && has_docker; then
            log_test "Loading go-ffmpeg-hls-swarm-container into Docker..."
            if docker load < "$container_path" >/dev/null 2>&1; then
                test_pass "go-ffmpeg-hls-swarm-container (load)"
            else
                test_fail "go-ffmpeg-hls-swarm-container (load)" "Failed to load into Docker"
            fi
        fi
    else
        test_fail "go-ffmpeg-hls-swarm-container" "Container path invalid"
    fi
else
    test_fail "go-ffmpeg-hls-swarm-container" "Build failed"
fi
```

**Add enhanced container test** (after swarm-client test):
```bash
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

#### Step 10.3: Create test-containers-env.sh

**File**: `scripts/nix-tests/test-containers-env.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Test container environment variable support

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

if ! is_linux || ! has_docker; then
    log_warn "Skipping container env var tests (requires Linux + Docker)"
    exit 0
fi

log_info "Testing container environment variable support for $SYSTEM"
log_info "This requires Docker/Podman..."
echo ""

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

print_summary
```

#### Step 10.4: Create test-iso.sh

**File**: `scripts/nix-tests/test-iso.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Test ISO image builds (Linux only)
# Includes KVM permission check for operational safety

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "Testing ISO image builds for $SYSTEM"

if ! is_linux; then
    log_warn "Skipping ISO tests (not on Linux)"
    test_skip "all-isos" "Not on Linux"
    print_summary
    exit 0
fi

log_info "This may take 10-15 minutes..."
echo ""

# Operational safety: Check KVM permissions (informational, ISO doesn't require KVM but good to know)
if ! check_kvm_permissions; then
    log_warn "KVM permissions check failed (ISO build doesn't require KVM, but VM testing will)"
    log_warn "This is informational only for ISO tests"
fi

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

print_summary
```

#### Step 10.5: Create test-cli.sh

**File**: `scripts/nix-tests/test-cli.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Test unified CLI entry point

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

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

#### Step 10.6: Update test-apps.sh

**File**: `scripts/nix-tests/test-apps.sh` (MODIFY)

**Location**: After line ~12 (CORE_APPS array)

**Add**:
```bash
# Unified CLI app (NEW)
test_app "up" "--help"

# Generate completion app (NEW)
test_app "generate-completion"
```

#### Step 10.7: Create Platform Parity Matrix Test

**File**: `scripts/nix-tests/test-platform-parity.sh` (NEW)

**Complete Implementation**:
```bash
#!/usr/bin/env bash
# Platform Parity Matrix Test
# Ensures universal packages exist on all platforms
# Explicitly fails if a universal package is missing on any platform

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
# shellcheck disable=SC1091
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)
readonly SYSTEM

log_info "════════════════════════════════════════════════════════════"
log_info "Platform Parity Matrix Test for $SYSTEM"
log_info "════════════════════════════════════════════════════════════"
echo ""

# Universal packages (must exist on all platforms)
readonly UNIVERSAL_PACKAGES=(
    "go-ffmpeg-hls-swarm"
    "test-origin"
    "swarm-client"
    "go-ffmpeg-hls-swarm-container"
    "test-origin-container"
    "swarm-client-container"
)

# Test each universal package
for pkg in "${UNIVERSAL_PACKAGES[@]}"; do
    test_platform_parity "$pkg" "all"
done

# Platform-specific packages (Linux only)
if is_linux; then
    readonly LINUX_ONLY_PACKAGES=(
        "test-origin-container-enhanced"
        "test-origin-iso"
    )

    for pkg in "${LINUX_ONLY_PACKAGES[@]}"; do
        log_test "Testing Linux-only package: $pkg..."
        if nix eval ".#packages.$SYSTEM.$pkg" --json >/dev/null 2>&1; then
            test_pass "$pkg (Linux, exists)"
        else
            test_fail "$pkg" "Should exist on Linux"
        fi
    done
else
    # On non-Linux, verify Linux-only packages are correctly omitted
    readonly LINUX_ONLY_PACKAGES=(
        "test-origin-container-enhanced"
        "test-origin-iso"
    )

    for pkg in "${LINUX_ONLY_PACKAGES[@]}"; do
        log_test "Testing Linux-only package: $pkg (should be omitted on $(uname))..."
        if nix eval ".#packages.$SYSTEM.$pkg" --json >/dev/null 2>&1; then
            test_fail "$pkg" "Should not exist on $(uname)"
        else
            test_pass "$pkg (correctly omitted on $(uname))"
        fi
    done
fi

print_summary

# Exit with failure if any universal packages are missing
if [[ $FAILED -gt 0 ]]; then
    log_error "Platform parity test failed - universal packages missing"
    exit 1
fi
```

#### Step 10.8: Update test-microvms.sh with KVM Check

**File**: `scripts/nix-tests/test-microvms.sh` (MODIFY)

**Location**: After KVM availability check (around line ~15)

**Add KVM permission check**:
```bash
if ! has_kvm; then
    log_warn "Skipping MicroVM tests (KVM not available)"
    exit 0
fi

# Operational safety: Check KVM permissions before attempting builds
if ! check_kvm_permissions; then
    log_error "KVM device exists but lacks proper permissions"
    log_error "This will cause misleading 'Build Failed' errors"
    log_error "Fix: sudo chmod 666 /dev/kvm (or add user to kvm group)"
    test_fail "kvm-permissions" "KVM permissions check failed"
    exit 1
fi
```

#### Step 10.9: Update test-all.sh

**File**: `scripts/nix-tests/test-all.sh` (MODIFY)

**Location**: After test-containers.sh (around line ~25)

**Add**:
```bash
# Platform parity matrix test (NEW)
"$SCRIPT_DIR/test-platform-parity.sh" || true
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
```

### Definition of Done

- [ ] All new test scripts exist
- [ ] All existing test scripts updated
- [ ] `test-all.sh` includes all new tests
- [ ] Tests pass on Linux
- [ ] Tests skip gracefully on non-Linux
- [ ] Test coverage includes all new features
- [ ] **Platform parity matrix test exists** and validates universal packages
- [ ] **KVM permission checks** are in place for VM/ISO tests
- [ ] **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings
- [ ] **Platform-specific packages** are correctly gated (Linux-only packages don't exist on macOS)
- [ ] **Golden path integration test exists** and validates end-to-end workflow
- [ ] **Golden path test passes**: Origin starts, metrics reachable, client can connect

### Validation Steps

1. **Run all tests**:
   ```bash
   ./scripts/nix-tests/test-all.sh
   ```

2. **Run individual test categories**:
   ```bash
   ./scripts/nix-tests/test-containers.sh
   ./scripts/nix-tests/test-cli.sh
   ./scripts/nix-tests/test-iso.sh
   ```

3. **Verify test coverage**:
   - All new packages tested
   - All new containers tested
   - Unified CLI tested
   - ISO tested (Linux only)

---

## Phase 11: Documentation Updates

### Overview

Update all documentation to reflect new features and improvements.

### Files to Create/Modify

1. **`README.md`** (MODIFY)
   - Add "Choice Points Table"
   - Add unified CLI documentation
   - Update quick start
   - Location: Various sections

2. **`docs/REFERENCE.md`** (NEW or MODIFY)
   - Technical reference
   - Flag reference
   - Location: New file or existing

3. **`docs/CI_CD.md`** (NEW or MODIFY)
   - Remote builders documentation
   - CI/CD examples
   - Location: New file or existing

### Detailed Steps

#### Step 11.1: Update README.md

**File**: `README.md` (MODIFY)

**Additions**:

1. **Choice Points Table** (after Quick Start):
   ```markdown
   ## Quick Reference

   | I want... | Run this Command | Requirements |
   |-----------|------------------|--------------|
   | **A local origin fast** | `nix run .#up -- default runner` | Any OS with Nix |
   | **A container origin** | `nix run .#up -- default container` | Linux to run |
   | **Max realism (Linux)** | `nix run .#up -- default vm` | Linux + KVM |
   | **Low-latency testing** | `nix run .#up -- low-latency runner` | Any OS with Nix |
   | **Stress test origin** | `nix run .#up -- stress runner` | Any OS with Nix |
   | **Help / Examples** | `nix run .#up -- --help` | Any OS with Nix |
   ```

2. **Unified CLI Section**:
   ```markdown
   ## Unified CLI Entry Point

   The `nix run .#up` command provides a single entry point for all deployments:

   - **Interactive mode** (TTY): Shows menu to select profile and type
   - **Non-interactive mode** (CI/non-TTY): Uses defaults (profile=default, type=runner)
   - **With arguments**: `nix run .#up -- <profile> <type>`

   See `nix run .#up -- --help` for full documentation.
   ```

3. **Shell Autocompletion**:
   ```markdown
   ## Shell Autocompletion

   Generate completion scripts from the single source of truth:

   ```bash
   nix run .#generate-completion
   source ./scripts/completion/bash-completion.sh  # Bash
   source ./scripts/completion/zsh-completion.sh    # Zsh
   ```
   ```

#### Step 11.2: Create/Update REFERENCE.md

**File**: `docs/REFERENCE.md` (NEW or MODIFY)

**Add**:
- Environment variable mapping table
- Flag reference
- Container healthcheck details
- Platform support matrix

#### Step 11.3: Create/Update CI_CD.md

**File**: `docs/CI_CD.md` (NEW or MODIFY)

**Add**:
- Remote builders setup (Tailscale, Determinate Systems)
- GitHub Actions examples
- Tiered checks usage
- ARM64 build instructions

### Definition of Done

- [ ] README.md updated with choice points table
- [ ] Unified CLI documented
- [ ] Shell autocompletion documented
- [ ] REFERENCE.md has technical details
- [ ] CI_CD.md has remote builder instructions
- [ ] All examples work

### Validation Steps

1. **Verify README examples**:
   ```bash
   # Test each command in choice points table
   nix run .#up -- --help
   nix run .#up -- default runner --help
   ```

2. **Verify documentation accuracy**:
   - All commands work as documented
   - All paths are correct
   - All examples are tested

---

## Validation and Testing Strategy

### Incremental Validation (After Each Phase)

**Before proceeding to next phase**, run:

1. **Gatekeeper validation** (if applicable):
   ```bash
   ./scripts/nix-tests/gatekeeper.sh
   ```

2. **Evaluation integrity check**:
   ```bash
   nix flake show 2>&1 | grep -qiE "broken|error" && echo "FAILED" || echo "PASSED"
   ```

3. **Package evaluation**:
   ```bash
   nix eval .#packages.$(nix eval --impure --expr 'builtins.currentSystem' --raw) --json >/dev/null
   ```

4. **Phase-specific validation** (see each phase's validation steps)

### Overall Validation

After all phases are complete:

1. **Gatekeeper validation**:
   ```bash
   ./scripts/nix-tests/gatekeeper.sh
   ```

2. **Dry run evaluation**:
   ```bash
   ./scripts/nix-tests/test-eval.sh
   ```

3. **Full test suite**:
   ```bash
   ./scripts/nix-tests/test-all.sh
   ```

4. **Platform parity matrix**:
   ```bash
   ./scripts/nix-tests/test-platform-parity.sh
   ```

5. **Nix flake check**:
   ```bash
   nix flake check .#checks.quick   # Fast
   nix flake check .#checks.full    # Comprehensive
   ```

6. **Build all packages**:
   ```bash
   # Universal packages
   nix build .#go-ffmpeg-hls-swarm-container
   nix build .#test-origin

   # Linux-only packages (on Linux)
   nix build .#test-origin-container-enhanced
   nix build .#test-origin-iso
   ```

7. **Test unified CLI**:
   ```bash
   nix run .#up -- --help
   nix run .#up -- default runner --help
   ```

8. **Test shell completion**:
   ```bash
   nix run .#generate-completion
   source ./scripts/completion/bash-completion.sh
   # Test tab completion
   ```

### Cross-Platform Validation

- **macOS**: Test universal packages, unified CLI, shell completion
- **Linux**: Test all packages including Linux-only variants
- **ARM64** (if available): Test multi-arch containers

### Performance Validation

- Quick checks complete in < 1 minute
- Build checks complete in < 10 minutes
- Full checks complete in < 60 minutes
- **Evaluation tests** complete in < 30 seconds (Phase 1.5)

### Safety Gate Validation Checklist

Before considering any phase complete, verify:

- [ ] **Gatekeeper passes** (if applicable to phase)
- [ ] **Evaluation integrity**: `nix flake show` shows no broken derivations
- [ ] **Package evaluation**: All packages evaluate without errors
- [ ] **Platform parity**: Universal packages exist on all platforms
- [ ] **Operational safety**: KVM/Docker permissions checked before use
- [ ] **Contract-first**: Dispatcher queries flake/source, not hardcoded lists

---

## Risk Mitigation

### Potential Issues

1. **Profile validation breaks existing code**
   - Mitigation: Phase 1.5 dry run catches this before builds
   - Rollback: Keep old config.nix pattern as backup
   - Safety gate: Gatekeeper script validates integrity

2. **Container builds fail on some platforms**
   - Mitigation: Use `lib.optionalAttrs` for platform-specific packages
   - Rollback: Make containers Linux-only if needed
   - Safety gate: Platform parity matrix test catches missing universal packages

3. **Unified CLI complexity**
   - Mitigation: Contract-first dispatcher auto-discovers profiles
   - Rollback: Can simplify to non-interactive only
   - Safety gate: Evaluation tests verify dispatcher logic

4. **Test script updates break CI**
   - Mitigation: Test scripts locally first, incremental validation
   - Rollback: Keep old test scripts as backup
   - Safety gate: Platform parity tests catch regressions

5. **KVM permission issues cause misleading errors**
   - Mitigation: Operational safety checks before VM builds
   - Rollback: N/A (check prevents issue)
   - Safety gate: `check_kvm_permissions()` in test scripts

6. **Broken derivations silently fail**
   - Mitigation: Evaluation integrity checks in every phase
   - Rollback: Fix broken derivation
   - Safety gate: `nix flake show` validation in gatekeeper

### Dependencies

- All phases depend on Phase 1 (Single Source of Truth)
- Phases 2-4 can be done in parallel
- Phase 10 depends on all previous phases
- Phase 11 depends on all implementation phases

---

## Success Criteria

The implementation is complete when:

1. ✅ All 11 phases are implemented (plus Phase 0 and Phase 1.5)
2. ✅ **Gatekeeper script passes**: `./scripts/nix-tests/gatekeeper.sh` exits with code 0
3. ✅ **Evaluation tests pass**: `./scripts/nix-tests/test-eval.sh` exits with code 0
4. ✅ **Platform parity tests pass**: `./scripts/nix-tests/test-platform-parity.sh` exits with code 0
5. ✅ All tests pass (`./scripts/nix-tests/test-all.sh`)
6. ✅ `nix flake check` passes (all tiers)
7. ✅ **Evaluation integrity**: `nix flake show` produces no "broken derivation" warnings
8. ✅ All packages build successfully
9. ✅ Unified CLI works in all modes (interactive, non-interactive, with args)
10. ✅ **Contract-first dispatcher**: New profiles auto-discovered without code changes
11. ✅ Shell autocompletion generates correctly
12. ✅ Documentation is updated and accurate
13. ✅ Cross-platform support works (macOS and Linux)
14. ✅ Container healthchecks are present
15. ✅ Docker Compose/Justfile work
16. ✅ **Operational safety**: KVM/Docker permission checks in place

---

## Next Steps After Implementation

1. **Review**: Code review of all changes
2. **Testing**: Comprehensive testing on multiple platforms
3. **Documentation**: Final documentation polish
4. **CI/CD**: Update CI to use new tiered checks
5. **Release**: Tag release with new features

---

**End of Implementation Plan**
