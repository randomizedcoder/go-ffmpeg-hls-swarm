# Nix Builds Interface Contracts

> **Type**: Contract Specification
> **Status**: Locked
> **Related**: [NIX_BUILDS_COMPREHENSIVE_DESIGN.md](NIX_BUILDS_COMPREHENSIVE_DESIGN.md), [NIX_BUILDS_IMPLEMENTATION_PLAN.md](NIX_BUILDS_IMPLEMENTATION_PLAN.md)

This document defines the interface contracts that must be followed by all phases of the Nix builds implementation. These contracts are locked before Phase 2 begins to prevent contract mismatches.

---

## 1. Canonical Naming Scheme

### Package Naming Pattern

**Test Origin Packages**:
- Pattern: `test-origin-<profile>-<type>`
- Types: `runner`, `container`, `container-enhanced` (Linux only), `vm` (Linux only)
- Examples:
  - `test-origin-default-runner`
  - `test-origin-low-latency-container`
  - `test-origin-stress-vm`
  - `test-origin-default-container-enhanced` (Linux only)

**Swarm Client Packages**:
- Pattern: `swarm-client-<profile>-<type>`
- Types: `runner`, `container`
- Examples:
  - `swarm-client-default-runner`
  - `swarm-client-stress-container`

**Main Binary Packages**:
- Pattern: `go-ffmpeg-hls-swarm-<type>`
- Types: (none for binary), `container`
- Examples:
  - `go-ffmpeg-hls-swarm` (binary)
  - `go-ffmpeg-hls-swarm-container`

### App Naming Pattern

- Unified CLI: `up` (dispatcher)
- Generate completion: `generate-completion`
- Core apps: `run`, `build`, `welcome`

### Contract Enforcement

All packages and apps MUST follow these patterns. No exceptions.

---

## 2. Unified CLI (`.#up`) Contract

### Input Contract

**Accepted Arguments**:
- `[profile] [type] [args...]`
  - `profile`: Must be from single source of truth (`profiles.nix`)
  - `type`: `runner`, `container`, `vm` (Linux only)
  - `args...`: All remaining args passed to underlying app
- `--help` or `-h`: Shows help and exits (no execution)

**Default Behavior**:
- `profile` defaults to `"default"` if not provided
- `type` defaults to `"runner"` if not provided

### Resolution Contract

**Resolution Pattern**:
- `test-origin-<profile>-<type>` → `nix run .#test-origin-<profile>-<type>`

**Platform Checks**:
- `vm` type fails with helpful error on non-Linux
- Error message must be actionable (e.g., "Try runner or container instead")

### Output Contract

**Dispatcher Transparency**:
- Always prints dispatcher info before execution
- Format: `"Executing: nix run .#<underlying> <args>"`
- Example: `"Executing: nix run .#test-origin-default-runner --help"`

**TTY Detection**:
- Non-TTY mode: No prompts, uses defaults silently (CI-friendly)
- TTY mode: Interactive menu if no args provided

### Contract Enforcement

CLI MUST follow this exact behavior. No deviations.

---

## 3. Gating Behavior Policy

### Policy: Omit Attribute

**Implementation**: Use `lib.optionalAttrs` to conditionally include attributes.

**Rationale**: Cleaner `nix flake show`, no confusing error messages for unsupported platforms.

### Implementation Pattern

```nix
# Linux-only packages
packages = {
  # ... universal packages ...
} // lib.optionalAttrs pkgs.stdenv.isLinux {
  test-origin-container-enhanced = ...;
  test-origin-iso = ...;
};

# KVM-only packages
packages = packages // lib.optionalAttrs (pkgs.stdenv.isLinux && hasKVM) {
  test-origin-vm = ...;
};
```

### Contract Enforcement

**NEVER** use `throw "error"` for platform gating. Always use `lib.optionalAttrs`.

---

## 4. Environment Variable Precedence Rule

### Policy: CLI Args Override Env Vars

**Rule**: CLI arguments take precedence over environment variables.

### Implementation Pattern

All containers MUST follow this pattern:

```bash
# Build args from env vars (if not overridden by CLI)
if ! echo "$*" | grep -qE '\s--clients\s'; then
    [ -n "${CLIENTS:-}" ] && ARGS+=(--clients "$CLIENTS")
fi

# CLI args come after env-var-derived args, so CLI overrides
exec ${lib.getExe package} "${ARGS[@]}" "$@"
```

### Contract Enforcement

All containers MUST follow this pattern. CLI always wins.

---

## 5. Flake Output Structure Contract

### Top-Level Keys (Allowlist)

Only these keys should appear in `nix flake show`:

- `packages`: All buildable outputs
- `apps`: All runnable applications
- `checks`: All validation checks
- `devShells`: Development environments
- `formatter`: Code formatter

### Contract Enforcement

**No accidental outputs**: Gatekeeper script validates this structure.

**Validation**:
```bash
FLAKE_OUTPUTS=$(nix flake show --json | jq -r 'keys[]' | sort)
EXPECTED_OUTPUTS="apps checks devShells formatter packages"
# Must match exactly
```

---

## Contract Validation

Before Phase 2 begins, all contracts must be:

- [x] Documented (this file)
- [x] Reviewed
- [x] Locked

---

## Contract Changes

**Policy**: Contracts are locked and should not change during implementation.

If a contract change is necessary:
1. Document the change and rationale
2. Update all affected phases
3. Re-validate all dependent phases

---

**Last Updated**: 2025-01-26  
**Status**: ✅ Locked
