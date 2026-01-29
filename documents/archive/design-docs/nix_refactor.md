# Nix Flake Refactoring Plan

> **Type**: Design Document
> **Status**: Proposal
> **Related**: [NIX_FLAKE_DESIGN.md](NIX_FLAKE_DESIGN.md), [TEST_ORIGIN.md](TEST_ORIGIN.md)

---

## Table of Contents

- [Current State Analysis](#current-state-analysis)
- [Main Features](#main-features)
- [Refactoring Goals](#refactoring-goals)
- [Proposed Improvements](#proposed-improvements)
- [Modularization Approaches](#modularization-approaches)
- [Implementation Plan](#implementation-plan)

---

## Current State Analysis

### File Structure

```
flake.nix                    # Main entry point (283 lines)
nix/
├── lib.nix                  # Shared metadata and helpers (35 lines)
├── package.nix              # Go package build (17 lines)
├── shell.nix                # Development shell (31 lines)
├── apps.nix                 # Runnable apps (26 lines)
├── checks.nix               # CI checks (32 lines)
├── test-origin/             # Test origin server components
│   ├── default.nix          # Entry point (105 lines)
│   ├── config.nix           # Configuration with profiles (430 lines)
│   ├── ffmpeg.nix           # FFmpeg argument builder (240+ lines)
│   ├── nginx.nix            # Nginx configuration
│   ├── runner.nix           # Local runner script (213+ lines)
│   ├── container.nix        # OCI container image
│   ├── microvm.nix          # MicroVM configuration
│   ├── nixos-module.nix     # NixOS systemd module
│   └── sysctl.nix           # Kernel tuning
├── swarm-client/            # Client deployment components
│   ├── default.nix          # Entry point (81 lines)
│   ├── config.nix           # Configuration with profiles (125 lines)
│   ├── runner.nix           # Local runner script
│   ├── container.nix        # OCI container image
│   ├── microvm.nix          # MicroVM configuration
│   ├── nixos-module.nix     # NixOS systemd module
│   └── sysctl.nix           # Kernel tuning
└── tests/
    └── integration.nix      # NixOS integration test (201 lines)
```

### Current Architecture

The flake follows a **modular component pattern** where:

1. **Core components** (`lib.nix`, `package.nix`, `shell.nix`, `apps.nix`, `checks.nix`) are simple, focused modules
2. **Feature components** (`test-origin/`, `swarm-client/`) are self-contained with their own:
   - Configuration system with profile support
   - Multiple deployment targets (runner, container, microvm, nixos-module)
   - Component-specific logic (ffmpeg args, nginx config, etc.)

### Strengths

✅ **Good separation of concerns**: Each file has a clear purpose
✅ **Profile system**: Flexible configuration via profiles
✅ **Reusable components**: Same config used across multiple deployment targets
✅ **Type safety**: Uses Nix's type system effectively
✅ **Documentation**: Well-documented with inline comments

### Areas for Improvement

⚠️ **Repetitive profile instantiation** in `flake.nix` (lines 125-143)
⚠️ **Duplicated utilities**: `deepMerge` function in config files
⚠️ **Large config files**: `test-origin/config.nix` is 430 lines
⚠️ **Similar patterns**: `test-origin/` and `swarm-client/` share structure but duplicate code
⚠️ **Limited reusability**: Profile system is component-specific, not generic
⚠️ **Manual app/package registration**: Each profile needs manual wiring in `flake.nix`

---

## Main Features

### 1. Core Package Build

- **Go module build** using `buildGoModule`
- **Source filtering** to exclude dev artifacts
- **Version injection** via ldflags
- **Multi-platform support** (x86_64/aarch64, Linux/macOS)

### 2. Development Shell

- **Complete toolchain**: Go, gopls, gotools, golangci-lint, delve
- **Runtime dependencies**: ffmpeg-full
- **Dev utilities**: curl, jq, nil
- **Welcome banner** on shell entry

### 3. Test Origin Server

- **Profile-based configuration**: default, low-latency, 4k-abr, stress-test, logged, debug, tap, tap-logged
- **FFmpeg HLS generation**: Modular argument builder with override support
- **Nginx serving**: High-performance config with dynamic cache headers
- **Multiple deployment targets**:
  - Local runner script
  - OCI container image
  - MicroVM (with user/tap networking)
  - NixOS systemd module
- **Derived calculations**: Storage estimates, segment lifetimes, cache timing

### 4. Swarm Client Deployment

- **Profile-based configuration**: default, stress, gentle, burst, extreme
- **Multiple deployment targets**: runner, container, microvm, nixos-module
- **Resource estimation**: Memory, FD limits, port requirements

### 5. CI Checks

- **Go checks**: format, vet, lint, test, build
- **Nix formatting**: nixfmt check
- **Integration tests**: NixOS VM-based testing (Linux only)

### 6. Apps

- **Welcome app**: Development shell banner
- **Build app**: Build and show output path
- **Run app**: Execute binary with FFmpeg available
- **Profile-specific apps**: Multiple test-origin and swarm-client variants

---

## Refactoring Goals

### Primary Goals

1. **Reduce repetition** in `flake.nix` profile instantiation
2. **Extract common utilities** to `lib.nix` (deepMerge, profile helpers)
3. **Simplify config files** by splitting large files into focused modules
4. **Create generic profile system** reusable across components
5. **Improve maintainability** with clearer abstractions

### Secondary Goals

6. **Better error messages** for invalid profiles/configs
7. **Type safety improvements** where possible
8. **Documentation** improvements for complex functions
9. **Performance** (minimal impact, but cleaner code may help)

### Constraints

- **Maintain functionality**: All existing features must work
- **Preserve API**: External usage (`nix run .#test-origin`, etc.) should remain
- **Backward compatibility**: Existing profiles and configs should work
- **Incremental**: Changes should be implementable step-by-step

---

## Proposed Improvements

### 1. Extract Common Utilities to `lib.nix`

**Current**: `deepMerge` is duplicated in `test-origin/config.nix` and potentially elsewhere.

**Proposed**: Move to `lib.nix` as a reusable utility.

```nix
# nix/lib.nix additions
deepMerge = base: overlay:
  let
    mergeAttr = name:
      if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
      then deepMerge base.${name} overlay.${name}
      else overlay.${name} or base.${name} or null;
    allKeys = builtins.attrNames (base // overlay);
  in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);

# Profile system helper
mkProfileConfig = { base, profiles, profile, overrides ? {} }:
  let
    profileConfig = profiles.${profile} or (throw "Unknown profile: ${profile}. Available: ${lib.concatStringsSep ", " (builtins.attrNames profiles)}");
    merged = deepMerge (deepMerge base profileConfig) overrides;
  in merged // {
    _profile = {
      name = profile;
      availableProfiles = builtins.attrNames profiles;
    };
  };
```

**Benefits**:
- Single source of truth for merge logic
- Reusable across components
- Easier to test and maintain

**Cons**:
- Slight indirection (but negligible performance impact)

---

### 2. Generic Profile System

**Current**: Each component (`test-origin`, `swarm-client`) implements its own profile system.

**Proposed**: Create a generic profile framework in `lib.nix`.

```nix
# nix/lib.nix
mkProfileSystem = { base, profiles }:
  rec {
    # Get config for a profile with optional overrides
    getConfig = profile: overrides: mkProfileConfig { inherit base profiles profile overrides; };

    # List all available profiles
    listProfiles = builtins.attrNames profiles;

    # Validate profile exists
    validateProfile = profile:
      if builtins.hasAttr profile profiles
      then true
      else throw "Unknown profile: ${profile}. Available: ${lib.concatStringsSep ", " listProfiles}";
  };
```

**Usage**:
```nix
# nix/test-origin/config.nix
{ profile ? "default", overrides ? {} }:
let
  profiles = { ... };  # Profile definitions
  base = { ... };      # Base config
  profileSystem = lib.mkProfileSystem { inherit base profiles; };
in profileSystem.getConfig profile overrides;
```

**Benefits**:
- Consistent profile API across components
- Less boilerplate
- Easier to add new components with profiles

**Cons**:
- Requires refactoring existing config files
- Slight learning curve for contributors

---

### 3. Reduce `flake.nix` Repetition

**Current**: Manual instantiation of each profile variant:

```nix
testOrigin = import ./nix/test-origin { inherit pkgs lib microvm; };
testOriginLowLatency = import ./nix/test-origin { inherit pkgs lib microvm; profile = "low-latency"; };
testOrigin4kAbr = import ./nix/test-origin { inherit pkgs lib microvm; profile = "4k-abr"; };
# ... 8 more similar lines
```

**Proposed**: Use a helper function to generate all profile variants:

```nix
# In flake.nix
let
  # Helper to generate all test-origin profiles
  mkTestOriginProfiles = profiles:
    lib.genAttrs profiles (profile:
      import ./nix/test-origin {
        inherit pkgs lib microvm;
        profile = profile;
      }
    );

  # Get available profiles from the default instance
  testOriginDefault = import ./nix/test-origin { inherit pkgs lib microvm; };
  testOriginProfiles = mkTestOriginProfiles testOriginDefault.availableProfiles;
in {
  packages = {
    # Use testOriginProfiles.default.runner, testOriginProfiles.low-latency.runner, etc.
  };
}
```

**Alternative (simpler)**: Use `lib.mapAttrs`:

```nix
let
  testOriginBase = import ./nix/test-origin { inherit pkgs lib microvm; };
  testOriginProfiles = lib.mapAttrs
    (name: _: import ./nix/test-origin { inherit pkgs lib microvm; profile = name; })
    (lib.genAttrs testOriginBase.availableProfiles (x: x));
in {
  packages = {
    test-origin = testOriginProfiles.default.runner;
    test-origin-low-latency = testOriginProfiles.low-latency.runner;
    # ... or use lib.mapAttrs again to generate all packages
  };
}
```

**Benefits**:
- DRY: Define profiles once, instantiate automatically
- Easier to add new profiles (just add to config, no flake.nix changes)
- Less error-prone (no copy-paste mistakes)

**Cons**:
- Slightly more complex flake.nix logic
- Need to handle profile names with dashes (use `lib.replaceStrings` or quoted keys)

---

### 4. Split Large Config Files

**Current**: `test-origin/config.nix` is 430 lines with:
- Profile definitions (~180 lines)
- Base configuration (~130 lines)
- Derived calculations (~50 lines)
- Cache timing (~20 lines)
- Merge logic (~50 lines)

**Proposed**: Split into focused modules:

```
nix/test-origin/
├── config/
│   ├── default.nix          # Main config entry point
│   ├── profiles.nix          # Profile definitions
│   ├── base.nix              # Base configuration
│   ├── derived.nix           # Derived calculations
│   └── cache.nix             # Cache timing logic
├── default.nix               # Component entry point (unchanged)
├── ffmpeg.nix
├── nginx.nix
└── ...
```

**Structure**:
```nix
# nix/test-origin/config/default.nix
{ profile ? "default", overrides ? {} }:
let
  profiles = import ./profiles.nix;
  base = import ./base.nix;
  # ... merge and return
in { ... };

# nix/test-origin/config/profiles.nix
{ ... }:
{
  default = { ... };
  low-latency = { ... };
  # ...
}

# nix/test-origin/config/base.nix
{ ... }:
{
  hls = { ... };
  server = { ... };
  # ...
}
```

**Benefits**:
- Easier to navigate and understand
- Smaller files are easier to review
- Can test individual pieces

**Cons**:
- More files to manage
- Slightly more import overhead (negligible)

---

### 5. Extract Common Component Patterns

**Current**: `test-origin/` and `swarm-client/` have similar structure but duplicate code.

**Proposed**: Create a generic component framework:

```nix
# nix/lib.nix
mkComponent = { name, config, ... }:
  {
    # Generic runner builder
    mkRunner = { script, runtimeInputs ? [] }:
      pkgs.writeShellApplication {
        name = "${name}-runner";
        inherit runtimeInputs;
        text = script;
      };

    # Generic container builder
    mkContainer = { ... }:
      # Shared container logic

    # Generic microvm builder
    mkMicrovm = { ... }:
      # Shared microvm logic
  };
```

**Usage**:
```nix
# nix/test-origin/default.nix
let
  component = lib.mkComponent {
    name = "test-origin";
    config = import ./config.nix { inherit profile; };
  };
in {
  runner = component.mkRunner { ... };
  container = component.mkContainer { ... };
  # ...
}
```

**Benefits**:
- Shared logic for common patterns
- Consistent API across components
- Less duplication

**Cons**:
- May be over-engineering if components diverge significantly
- Need to balance abstraction vs. flexibility

---

### 6. Improve Error Messages

**Current**: Generic errors like "Unknown profile: X".

**Proposed**: More helpful error messages:

```nix
# In lib.nix
validateProfile = { profiles, profile }:
  if builtins.hasAttr profile profiles
  then true
  else
    let
      available = builtins.attrNames profiles;
      suggestions = lib.closestMatch profile available;  # If we implement fuzzy matching
    in
      throw ''
        Unknown profile: ${profile}

        Available profiles:
        ${lib.concatMapStringsSep "\n" (p: "  - ${p}") available}

        ${lib.optionalString (suggestions != []) "Did you mean: ${lib.head suggestions}?"}
      '';
```

**Benefits**:
- Better developer experience
- Faster debugging

**Cons**:
- Slightly more complex code
- May need fuzzy matching library (or simple implementation)

---

## Modularization Approaches

### Approach A: Minimal Changes (Recommended for Phase 1)

**Focus**: Extract utilities, reduce repetition, keep structure mostly the same.

**Changes**:
1. Move `deepMerge` to `lib.nix`
2. Use `lib.mapAttrs` to generate profile variants in `flake.nix`
3. Add profile validation helpers to `lib.nix`
4. Split `test-origin/config.nix` into `config/` subdirectory

**Pros**:
- ✅ Low risk
- ✅ Easy to review
- ✅ Maintains existing structure
- ✅ Can be done incrementally

**Cons**:
- ⚠️ Doesn't address all duplication
- ⚠️ Still some manual wiring needed

**Effort**: Low (2-4 hours)

---

### Approach B: Generic Profile System (Recommended for Phase 2)

**Focus**: Create reusable profile framework, refactor components to use it.

**Changes**:
1. Implement `mkProfileSystem` in `lib.nix`
2. Refactor `test-origin/config.nix` and `swarm-client/config.nix` to use it
3. Create generic component builder helpers
4. Automate profile variant generation in `flake.nix`

**Pros**:
- ✅ Eliminates profile system duplication
- ✅ Easier to add new components
- ✅ More consistent API
- ✅ Better maintainability

**Cons**:
- ⚠️ More refactoring required
- ⚠️ Need to test all profiles still work
- ⚠️ Slightly more abstraction

**Effort**: Medium (4-8 hours)

---

### Approach C: Full Component Framework (Future Consideration)

**Focus**: Create a complete framework for building Nix components with profiles.

**Changes**:
1. Generic component builder with plugin system
2. Automatic app/package generation from profiles
3. Shared deployment target builders (container, microvm, etc.)
4. Configuration schema validation

**Pros**:
- ✅ Maximum reusability
- ✅ Very consistent patterns
- ✅ Easy to add new components

**Cons**:
- ⚠️ Significant refactoring
- ⚠️ May be over-engineering
- ⚠️ Harder to customize for edge cases
- ⚠️ Learning curve for contributors

**Effort**: High (1-2 days)

---

### Approach D: Hybrid (Recommended Overall Strategy)

**Phase 1** (Immediate): Approach A - Quick wins, low risk
**Phase 2** (Next): Approach B - Generic profile system
**Phase 3** (If needed): Approach C - Full framework for specific pain points

**Pros**:
- ✅ Incremental improvements
- ✅ Can stop at any phase if sufficient
- ✅ Lower risk overall
- ✅ Learn from each phase

**Cons**:
- ⚠️ Multiple refactoring passes
- ⚠️ Need to maintain intermediate states

---

## Implementation Plan

### Phase 1: Quick Wins (Week 1)

**Goal**: Reduce repetition and extract common utilities.

1. **Extract `deepMerge` to `lib.nix`**
   - Move function from `test-origin/config.nix`
   - Update all usages
   - Test that configs still work

2. **Reduce `flake.nix` repetition**
   - Use `lib.mapAttrs` to generate profile variants
   - Test all profiles still accessible
   - Verify apps and packages work

3. **Split `test-origin/config.nix`**
   - Create `config/` subdirectory
   - Split into `profiles.nix`, `base.nix`, `derived.nix`, `cache.nix`
   - Update imports
   - Test all profiles

**Deliverables**:
- ✅ `lib.nix` with `deepMerge`
- ✅ Cleaner `flake.nix` with less repetition
- ✅ Split config files

**Testing**:
- Run `nix flake check`
- Test all profile apps: `nix run .#test-origin-*`
- Verify containers and microvms still build

---

### Phase 2: Generic Profile System (Week 2)

**Goal**: Create reusable profile framework.

1. **Implement `mkProfileSystem` in `lib.nix`**
   - Profile validation
   - Config merging with overrides
   - Profile metadata

2. **Refactor `test-origin/config.nix`**
   - Use `mkProfileSystem`
   - Simplify merge logic
   - Test all profiles

3. **Refactor `swarm-client/config.nix`**
   - Use `mkProfileSystem`
   - Ensure consistency with test-origin

4. **Improve error messages**
   - Better profile validation errors
   - Helpful suggestions

**Deliverables**:
- ✅ Generic profile system
- ✅ Refactored config files
- ✅ Better error messages

**Testing**:
- Test invalid profiles show helpful errors
- Verify all existing profiles work
- Test override functionality

---

### Phase 3: Component Helpers (Optional, Week 3+)

**Goal**: Extract common component patterns.

1. **Analyze common patterns**
   - Identify shared logic between test-origin and swarm-client
   - Document patterns

2. **Create component helpers**
   - Generic runner builder
   - Shared container logic (if applicable)
   - Shared microvm setup (if applicable)

3. **Refactor components to use helpers**
   - Update test-origin
   - Update swarm-client
   - Ensure no functionality loss

**Deliverables**:
- ✅ Component helper library
- ✅ Refactored components
- ✅ Documentation

**Testing**:
- Full integration test suite
- Performance comparison (should be same or better)

---

## Detailed Refactoring Examples

### Example 1: Extracting `deepMerge`

**Before** (`test-origin/config.nix`):
```nix
deepMerge = base: overlay:
  let
    mergeAttr = name:
      if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
      then deepMerge base.${name} overlay.${name}
      else overlay.${name} or base.${name} or null;
    allKeys = builtins.attrNames (base // overlay);
  in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);
```

**After** (`nix/lib.nix`):
```nix
# Deep merge two attribute sets, recursively merging nested sets
deepMerge = base: overlay:
  let
    mergeAttr = name:
      if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
      then deepMerge base.${name} overlay.${name}
      else overlay.${name} or base.${name} or null;
    allKeys = builtins.attrNames (base // overlay);
  in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);
```

**Usage** (`test-origin/config.nix`):
```nix
{ profile ? "default", overrides ? {}, lib }:
let
  # Use lib.deepMerge instead of local definition
  mergedConfig = lib.deepMerge (lib.deepMerge baseConfig profileConfig) overrides;
in { ... }
```

---

### Example 2: Generating Profile Variants

**Before** (`flake.nix`):
```nix
testOrigin = import ./nix/test-origin { inherit pkgs lib microvm; };
testOriginLowLatency = import ./nix/test-origin { inherit pkgs lib microvm; profile = "low-latency"; };
testOrigin4kAbr = import ./nix/test-origin { inherit pkgs lib microvm; profile = "4k-abr"; };
# ... 8 more lines
```

**After** (`flake.nix`):
```nix
let
  # Get available profiles from default instance
  testOriginDefault = import ./nix/test-origin { inherit pkgs lib microvm; };

  # Generate all profile variants
  testOriginProfiles = lib.mapAttrs
    (name: _: import ./nix/test-origin {
      inherit pkgs lib microvm;
      profile = name;
    })
    (lib.genAttrs testOriginDefault.availableProfiles (x: x));
in {
  packages = {
    # Use testOriginProfiles.default.runner, etc.
    test-origin = testOriginProfiles.default.runner;
    test-origin-low-latency = testOriginProfiles.low-latency.runner;
    # Or generate all automatically:
    # test-origin = testOriginProfiles.default.runner;
    # } // lib.mapAttrs' (name: value: {
    #   name = "test-origin-${lib.replaceStrings ["_"] ["-"] name}";
    #   value = value.runner;
    # }) (lib.removeAttrs testOriginProfiles ["default"]);
  };
}
```

**Even cleaner** (if we want automatic package generation):
```nix
let
  testOriginProfiles = # ... as above

  # Helper to generate package names from profile names
  mkPackageName = baseName: profileName:
    if profileName == "default"
    then baseName
    else "${baseName}-${lib.replaceStrings ["_"] ["-"] profileName}";

  # Auto-generate all test-origin packages
  testOriginPackages = lib.mapAttrs'
    (name: value: {
      name = mkPackageName "test-origin" name;
      value = value.runner;
    })
    testOriginProfiles;
in {
  packages = testOriginPackages // {
    # Other packages...
  };
}
```

---

### Example 3: Generic Profile System

**Before** (`test-origin/config.nix`):
```nix
{ profile ? "default", overrides ? {} }:
let
  profiles = { default = {...}; low-latency = {...}; ... };
  baseConfig = {...};
  deepMerge = ...;  # Local definition
  profileConfig = profiles.${profile} or (throw "Unknown profile: ${profile}");
  mergedConfig = deepMerge (deepMerge baseConfig profileConfig) overrides;
in mergedConfig // {
  _profile = {
    name = profile;
    availableProfiles = builtins.attrNames profiles;
  };
}
```

**After** (`test-origin/config.nix`):
```nix
{ profile ? "default", overrides ? {}, lib }:
let
  profiles = import ./profiles.nix;
  base = import ./base.nix;

  # Use generic profile system
  config = lib.mkProfileSystem {
    inherit base profiles;
  }.getConfig profile overrides;
in config // {
  # Component-specific additions (derived, cache, etc.)
  inherit (import ./derived.nix { config = config; }) derived;
  inherit (import ./cache.nix { config = config; }) cache;
}
```

**New** (`nix/lib.nix`):
```nix
# Generic profile system builder
mkProfileSystem = { base, profiles }:
  rec {
    # Deep merge helper (extracted)
    deepMerge = base: overlay:
      # ... (as shown in Example 1)

    # Get config for a profile with optional overrides
    getConfig = profile: overrides:
      let
        profileConfig = profiles.${profile} or (
          throw "Unknown profile: ${profile}. Available: ${lib.concatStringsSep ", " (builtins.attrNames profiles)}"
        );
        merged = deepMerge (deepMerge base profileConfig) overrides;
      in merged // {
        _profile = {
          name = profile;
          availableProfiles = builtins.attrNames profiles;
        };
      };

    # List all available profiles
    listProfiles = builtins.attrNames profiles;

    # Validate profile exists
    validateProfile = profile:
      if builtins.hasAttr profile profiles
      then true
      else throw "Unknown profile: ${profile}. Available: ${lib.concatStringsSep ", " listProfiles}";
  };
```

---

## Migration Checklist

### Phase 1 Checklist

- [ ] Extract `deepMerge` to `lib.nix`
- [ ] Update `test-origin/config.nix` to use `lib.deepMerge`
- [ ] Update `swarm-client/config.nix` if it has `deepMerge`
- [ ] Test all profiles still work
- [ ] Refactor `flake.nix` profile instantiation
- [ ] Test all apps: `nix run .#test-origin-*`
- [ ] Test all packages: `nix build .#test-origin-*`
- [ ] Split `test-origin/config.nix` into subdirectory
- [ ] Update imports in `test-origin/default.nix`
- [ ] Run full test suite: `nix flake check`
- [ ] Update documentation if needed

### Phase 2 Checklist

- [ ] Implement `mkProfileSystem` in `lib.nix`
- [ ] Add tests for profile system (if possible)
- [ ] Refactor `test-origin/config.nix` to use `mkProfileSystem`
- [ ] Test all test-origin profiles
- [ ] Refactor `swarm-client/config.nix` to use `mkProfileSystem`
- [ ] Test all swarm-client profiles
- [ ] Improve error messages
- [ ] Test error cases (invalid profiles)
- [ ] Update documentation
- [ ] Run full test suite

### Phase 3 Checklist (Optional)

- [ ] Analyze common patterns between components
- [ ] Design component helper API
- [ ] Implement component helpers in `lib.nix`
- [ ] Refactor `test-origin/` to use helpers
- [ ] Refactor `swarm-client/` to use helpers
- [ ] Test all functionality
- [ ] Performance comparison
- [ ] Update documentation
- [ ] Consider if further abstraction is needed

---

## Testing Strategy

### Unit Testing (Manual)

1. **Profile System**:
   ```bash
   # Test profile validation
   nix eval --expr '(import ./nix/lib.nix { pkgs = import <nixpkgs> {}; lib = (import <nixpkgs> {}).lib; }).mkProfileSystem { base = {}; profiles = { default = {}; }; }.validateProfile "default"'

   # Test config merging
   nix eval --expr '...'  # Test getConfig with overrides
   ```

2. **Config Files**:
   ```bash
   # Test each profile loads
   nix eval .#packages.x86_64-linux.test-origin
   nix eval .#packages.x86_64-linux.test-origin-low-latency
   # ... all profiles
   ```

3. **Flake Outputs**:
   ```bash
   nix flake check
   nix flake show
   ```

### Integration Testing

1. **Build all packages**:
   ```bash
   nix build .#packages.x86_64-linux.test-origin
   nix build .#packages.x86_64-linux.test-origin-container
   # ... all variants
   ```

2. **Run all apps**:
   ```bash
   nix run .#test-origin --help
   nix run .#test-origin-low-latency --help
   # ... all profiles
   ```

3. **Integration test**:
   ```bash
   nix build .#checks.x86_64-linux.integration-test
   ```

### Regression Testing

- Compare outputs before/after refactoring:
  ```bash
  # Before refactor
  nix build .#test-origin-container
  nix-store --query --tree ./result > before.txt

  # After refactor
  nix build .#test-origin-container
  nix-store --query --tree ./result > after.txt

  diff before.txt after.txt  # Should be minimal differences
  ```

---

## Risks and Mitigations

### Risk 1: Breaking Existing Functionality

**Mitigation**:
- Incremental changes with testing at each step
- Keep old code until new code is verified
- Comprehensive test suite before/after

### Risk 2: Over-Abstraction

**Mitigation**:
- Start with minimal changes (Phase 1)
- Only abstract when patterns are clear
- Keep components flexible

### Risk 3: Performance Impact

**Mitigation**:
- Profile system adds minimal overhead (just function calls)
- Test build times before/after
- Nix evaluation is typically fast anyway

### Risk 4: Learning Curve

**Mitigation**:
- Good documentation
- Clear examples
- Incremental rollout

---

## Success Metrics

### Code Quality

- ✅ Reduced lines of code in `flake.nix` (target: -50%)
- ✅ Reduced duplication (target: eliminate `deepMerge` duplication)
- ✅ Smaller config files (target: <200 lines per file)

### Maintainability

- ✅ Easier to add new profiles (target: no `flake.nix` changes needed)
- ✅ Consistent patterns across components
- ✅ Better error messages

### Functionality

- ✅ All existing features work
- ✅ All profiles accessible
- ✅ All deployment targets build

---

## Conclusion

The current Nix flake is well-structured but has opportunities for improvement:

1. **Immediate wins** (Phase 1): Extract utilities, reduce repetition
2. **Medium-term** (Phase 2): Generic profile system for consistency
3. **Long-term** (Phase 3): Component helpers if patterns emerge

The recommended approach is **incremental refactoring** starting with Phase 1, which provides immediate benefits with low risk. Phase 2 can follow if the generic profile system proves valuable, and Phase 3 only if clear patterns emerge that justify further abstraction.

**Next Steps**:
1. Review this document
2. Decide on approach (recommended: Hybrid, starting with Phase 1)
3. Create issues/tasks for each phase
4. Begin implementation with Phase 1

---

## Appendix: Code Statistics

### Current State

- `flake.nix`: 283 lines
- `nix/lib.nix`: 35 lines
- `nix/test-origin/config.nix`: 430 lines
- `nix/swarm-client/config.nix`: 125 lines
- Total: ~2000+ lines across all Nix files

### Target State (Phase 1)

- `flake.nix`: ~200 lines (estimated -30%)
- `nix/lib.nix`: ~100 lines (+65 lines for utilities)
- `nix/test-origin/config/`: 4 files, ~100-150 lines each
- Total: Similar, but better organized

### Target State (Phase 2)

- Similar line counts, but more reusable code
- Easier to add new components
- Consistent patterns
