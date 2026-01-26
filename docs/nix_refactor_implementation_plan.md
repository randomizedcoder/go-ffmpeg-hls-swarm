# Nix Refactoring Implementation Plan

> **Type**: Implementation Plan
> **Status**: Ready for Review
> **Related**: [nix_refactor.md](nix_refactor.md)

This document provides a detailed, step-by-step implementation plan for refactoring the Nix flake codebase. It includes specific file paths, line numbers, function names, and exact changes to make.

---

## Table of Contents

- [Overview](#overview)
- [Phase 1: Quick Wins](#phase-1-quick-wins)
- [Phase 2: Generic Profile System](#phase-2-generic-profile-system)
- [Testing Checklist](#testing-checklist)
- [Rollback Plan](#rollback-plan)

---

## Overview

This plan implements the refactoring described in [nix_refactor.md](nix_refactor.md), focusing on:
- **Phase 1**: Extract utilities, reduce repetition, split large files
- **Phase 2**: Create generic profile system, refactor components

**Estimated Time**:
- Phase 1: 2-4 hours
- Phase 2: 4-8 hours

**Risk Level**: Low (incremental changes with testing at each step)

---

## Phase 1: Quick Wins

### Step 1.1: Extract `deepMerge` to `lib.nix`

**Goal**: Move the `deepMerge` function from `test-origin/config.nix` to `lib.nix` for reuse.

#### 1.1.1: Add `deepMerge` to `lib.nix`

**File**: `nix/lib.nix`

**Current State**: Lines 1-35 (no `deepMerge` function)

**Action**: Add `deepMerge` function after line 15 (after `devUtils` definition, before `mkGoCheck`)

**Insert at line 16**:
```nix
  # Deep merge two attribute sets, recursively merging nested sets
  # Used for merging base config, profile config, and overrides
  deepMerge = base: overlay:
    let
      mergeAttr = name:
        if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
        then deepMerge base.${name} overlay.${name}
        else overlay.${name} or base.${name} or null;
      allKeys = builtins.attrNames (base // overlay);
    in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);
```

**Result**: `lib.nix` will have `deepMerge` available for all components.

---

#### 1.1.2: Update `test-origin/config.nix` to use `lib.deepMerge`

**File**: `nix/test-origin/config.nix`

**Current State**:
- Lines 317-324: Local `deepMerge` function definition
- Line 328: Usage of `deepMerge`

**Action 1**: Update function signature to accept `lib` parameter

**Change line 9** from:
```nix
{ profile ? "default", overrides ? {} }:
```

**To**:
```nix
{ profile ? "default", overrides ? {}, lib }:
```

**Action 2**: Remove local `deepMerge` definition

**Delete lines 314-324** (the entire section with comment and function):
```nix
  # ═══════════════════════════════════════════════════════════════════════════
  # Deep merge function for nested attribute sets
  # ═══════════════════════════════════════════════════════════════════════════
  deepMerge = base: overlay:
    let
      mergeAttr = name:
        if builtins.isAttrs (base.${name} or null) && builtins.isAttrs (overlay.${name} or null)
        then deepMerge base.${name} overlay.${name}
        else overlay.${name} or base.${name} or null;
      allKeys = builtins.attrNames (base // overlay);
    in builtins.listToAttrs (map (name: { inherit name; value = mergeAttr name; }) allKeys);
```

**Action 3**: Update usage to use `lib.deepMerge`

**Change line 328** (now will be around line 314 after deletion) from:
```nix
  mergedConfig = deepMerge (deepMerge baseConfig profileConfig) overrides;
```

**To**:
```nix
  mergedConfig = lib.deepMerge (lib.deepMerge baseConfig profileConfig) overrides;
```

**Action 4**: Update `test-origin/default.nix` to pass `lib`

**File**: `nix/test-origin/default.nix`

**Current State**: Line 28 imports config without `lib`

**Change line 28** from:
```nix
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
  };
```

**To**:
```nix
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
    lib = lib;
  };
```

---

#### 1.1.3: Verify `swarm-client/config.nix` doesn't need changes

**File**: `nix/swarm-client/config.nix`

**Action**: Check if `swarm-client/config.nix` uses `deepMerge`

**Result**: Based on review, `swarm-client/config.nix` uses simple attribute set merge (`base // overrides`) on line 90, so no changes needed.

---

#### 1.1.4: Test Step 1.1

**Commands**:
```bash
# Test that config still loads
nix eval --expr '(import ./nix/test-origin/config.nix { profile = "default"; lib = (import <nixpkgs> {}).lib; })._profile.name'

# Test that deepMerge works
nix eval --expr '(import ./nix/lib.nix { pkgs = import <nixpkgs> {}; lib = (import <nixpkgs> {}).lib; }).deepMerge { a = { b = 1; }; } { a = { c = 2; }; }'

# Test that test-origin still works
nix eval .#packages.x86_64-linux.test-origin
```

**Expected**: All commands should succeed without errors.

---

### Step 1.2: Reduce `flake.nix` Repetition

**Goal**: Use `lib.mapAttrs` to automatically generate profile variants instead of manual instantiation.

#### 1.2.1: Create helper to generate test-origin profiles

**File**: `flake.nix`

**Current State**: Lines 125-136 manually instantiate each profile

**Action**: Replace lines 125-136 with automatic generation

**Replace lines 125-136**:
```nix
        # Test origin server components (with profile support and MicroVM)
        testOrigin = import ./nix/test-origin { inherit pkgs lib microvm; };
        testOriginLowLatency = import ./nix/test-origin { inherit pkgs lib microvm; profile = "low-latency"; };
        testOrigin4kAbr = import ./nix/test-origin { inherit pkgs lib microvm; profile = "4k-abr"; };
        testOriginStress = import ./nix/test-origin { inherit pkgs lib microvm; profile = "stress-test"; };

        # Logging-enabled profiles for performance analysis
        testOriginLogged = import ./nix/test-origin { inherit pkgs lib microvm; profile = "logged"; };
        testOriginDebug = import ./nix/test-origin { inherit pkgs lib microvm; profile = "debug"; };

        # TAP networking profiles (high performance, requires make network-setup)
        testOriginTap = import ./nix/test-origin { inherit pkgs lib microvm; profile = "tap"; };
        testOriginTapLogged = import ./nix/test-origin { inherit pkgs lib microvm; profile = "tap-logged"; };
```

**With**:
```nix
        # Test origin server components (with profile support and MicroVM)
        # Get available profiles from default instance
        testOriginDefault = import ./nix/test-origin { inherit pkgs lib microvm; };

        # Generate all profile variants automatically
        testOriginProfiles = lib.mapAttrs
          (name: _: import ./nix/test-origin {
            inherit pkgs lib microvm;
            profile = name;
          })
          (lib.genAttrs testOriginDefault.availableProfiles (x: x));
```

**Note**: This creates an attribute set where keys are profile names (e.g., `testOriginProfiles.default`, `testOriginProfiles.low-latency`).

---

#### 1.2.2: Update test-origin package references

**File**: `flake.nix`

**Current State**: Lines 153-175 reference individual profile variables

**Action**: Update to use `testOriginProfiles` attribute set

**Replace lines 153-165**:
```nix
          # Test origin server packages (default profile)
          test-origin = testOrigin.runner;
          test-origin-container = testOrigin.container;

          # Profile-specific test origins
          test-origin-low-latency = testOriginLowLatency.runner;
          test-origin-4k-abr = testOrigin4kAbr.runner;
          test-origin-stress = testOriginStress.runner;

          # Logging-enabled profiles for performance analysis
          test-origin-logged = testOriginLogged.runner;
          test-origin-debug = testOriginDebug.runner;
```

**With**:
```nix
          # Test origin server packages (default profile)
          test-origin = testOriginProfiles.default.runner;
          test-origin-container = testOriginProfiles.default.container;

          # Profile-specific test origins
          test-origin-low-latency = testOriginProfiles.low-latency.runner;
          test-origin-4k-abr = testOriginProfiles."4k-abr".runner;
          test-origin-stress = testOriginProfiles.stress-test.runner;

          # Logging-enabled profiles for performance analysis
          test-origin-logged = testOriginProfiles.logged.runner;
          test-origin-debug = testOriginProfiles.debug.runner;
```

**Replace lines 167-175**:
```nix
          # MicroVM packages (Linux only, requires KVM)
          test-origin-vm = testOrigin.microvm.vm or (throw "MicroVM not available - requires microvm input");
          test-origin-vm-low-latency = testOriginLowLatency.microvm.vm or null;
          test-origin-vm-stress = testOriginStress.microvm.vm or null;
          test-origin-vm-logged = testOriginLogged.microvm.vm or null;
          test-origin-vm-debug = testOriginDebug.microvm.vm or null;

          # TAP networking MicroVMs (high performance, requires make network-setup)
          test-origin-vm-tap = testOriginTap.microvm.vm or null;
          test-origin-vm-tap-logged = testOriginTapLogged.microvm.vm or null;
```

**With**:
```nix
          # MicroVM packages (Linux only, requires KVM)
          test-origin-vm = testOriginProfiles.default.microvm.vm or (throw "MicroVM not available - requires microvm input");
          test-origin-vm-low-latency = testOriginProfiles.low-latency.microvm.vm or null;
          test-origin-vm-stress = testOriginProfiles.stress-test.microvm.vm or null;
          test-origin-vm-logged = testOriginProfiles.logged.microvm.vm or null;
          test-origin-vm-debug = testOriginProfiles.debug.microvm.vm or null;

          # TAP networking MicroVMs (high performance, requires make network-setup)
          test-origin-vm-tap = testOriginProfiles.tap.microvm.vm or null;
          test-origin-vm-tap-logged = testOriginProfiles.tap-logged.microvm.vm or null;
```

---

#### 1.2.3: Update test-origin app references

**File**: `flake.nix`

**Current State**: Lines 194-243 reference individual profile variables for apps

**Action**: Update to use `testOriginProfiles` attribute set

**Replace lines 194-209**:
```nix
          # Test origin server apps (different profiles)
          test-origin = {
            type = "app";
            program = "${testOrigin.runner}/bin/test-hls-origin";
          };
          test-origin-low-latency = {
            type = "app";
            program = "${testOriginLowLatency.runner}/bin/test-hls-origin";
          };
          test-origin-4k-abr = {
            type = "app";
            program = "${testOrigin4kAbr.runner}/bin/test-hls-origin";
          };
          test-origin-stress = {
            type = "app";
            program = "${testOriginStress.runner}/bin/test-hls-origin";
          };
```

**With**:
```nix
          # Test origin server apps (different profiles)
          test-origin = {
            type = "app";
            program = "${testOriginProfiles.default.runner}/bin/test-hls-origin";
          };
          test-origin-low-latency = {
            type = "app";
            program = "${testOriginProfiles.low-latency.runner}/bin/test-hls-origin";
          };
          test-origin-4k-abr = {
            type = "app";
            program = "${testOriginProfiles."4k-abr".runner}/bin/test-hls-origin";
          };
          test-origin-stress = {
            type = "app";
            program = "${testOriginProfiles.stress-test.runner}/bin/test-hls-origin";
          };
```

**Replace lines 211-219**:
```nix
          # Logging-enabled profiles for performance analysis
          test-origin-logged = {
            type = "app";
            program = "${testOriginLogged.runner}/bin/test-hls-origin";
          };
          test-origin-debug = {
            type = "app";
            program = "${testOriginDebug.runner}/bin/test-hls-origin";
          };
```

**With**:
```nix
          # Logging-enabled profiles for performance analysis
          test-origin-logged = {
            type = "app";
            program = "${testOriginProfiles.logged.runner}/bin/test-hls-origin";
          };
          test-origin-debug = {
            type = "app";
            program = "${testOriginProfiles.debug.runner}/bin/test-hls-origin";
          };
```

**Replace lines 221-243**:
```nix
          # MicroVM apps (Linux only, requires KVM)
          test-origin-vm = {
            type = "app";
            program = "${testOrigin.microvm.runScript}";
          };
          test-origin-vm-logged = {
            type = "app";
            program = "${testOriginLogged.microvm.runScript}";
          };
          test-origin-vm-debug = {
            type = "app";
            program = "${testOriginDebug.microvm.runScript}";
          };

          # TAP networking MicroVM apps (high performance)
          test-origin-vm-tap = {
            type = "app";
            program = "${testOriginTap.microvm.runScript}";
          };
          test-origin-vm-tap-logged = {
            type = "app";
            program = "${testOriginTapLogged.microvm.runScript}";
          };
```

**With**:
```nix
          # MicroVM apps (Linux only, requires KVM)
          test-origin-vm = {
            type = "app";
            program = "${testOriginProfiles.default.microvm.runScript}";
          };
          test-origin-vm-logged = {
            type = "app";
            program = "${testOriginProfiles.logged.microvm.runScript}";
          };
          test-origin-vm-debug = {
            type = "app";
            program = "${testOriginProfiles.debug.microvm.runScript}";
          };

          # TAP networking MicroVM apps (high performance)
          test-origin-vm-tap = {
            type = "app";
            program = "${testOriginProfiles.tap.microvm.runScript}";
          };
          test-origin-vm-tap-logged = {
            type = "app";
            program = "${testOriginProfiles.tap-logged.microvm.runScript}";
          };
```

---

#### 1.2.4: Create helper for swarm-client profiles

**File**: `flake.nix`

**Current State**: Lines 138-143 manually instantiate swarm-client profiles

**Action**: Replace with automatic generation

**Replace lines 138-143**:
```nix
        # Swarm client components (with profile support)
        swarmClient = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; };
        swarmClientStress = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "stress"; };
        swarmClientGentle = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "gentle"; };
        swarmClientBurst = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "burst"; };
        swarmClientExtreme = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; profile = "extreme"; };
```

**With**:
```nix
        # Swarm client components (with profile support)
        # Get available profiles from default instance
        swarmClientDefault = import ./nix/swarm-client { inherit pkgs lib; swarmBinary = package; };

        # Generate all profile variants automatically
        swarmClientProfiles = lib.mapAttrs
          (name: _: import ./nix/swarm-client {
            inherit pkgs lib;
            swarmBinary = package;
            profile = name;
          })
          (lib.genAttrs swarmClientDefault.availableProfiles (x: x));
```

---

#### 1.2.5: Update swarm-client package references

**File**: `flake.nix`

**Current State**: Lines 177-185 reference individual swarm-client variables

**Action**: Update to use `swarmClientProfiles` attribute set

**Replace lines 177-185**:
```nix
          # Swarm client packages (default profile)
          swarm-client = swarmClient.runner;
          swarm-client-container = swarmClient.container;

          # Profile-specific swarm clients
          swarm-client-stress = swarmClientStress.runner;
          swarm-client-gentle = swarmClientGentle.runner;
          swarm-client-burst = swarmClientBurst.runner;
          swarm-client-extreme = swarmClientExtreme.runner;
```

**With**:
```nix
          # Swarm client packages (default profile)
          swarm-client = swarmClientProfiles.default.runner;
          swarm-client-container = swarmClientProfiles.default.container;

          # Profile-specific swarm clients
          swarm-client-stress = swarmClientProfiles.stress.runner;
          swarm-client-gentle = swarmClientProfiles.gentle.runner;
          swarm-client-burst = swarmClientProfiles.burst.runner;
          swarm-client-extreme = swarmClientProfiles.extreme.runner;
```

---

#### 1.2.6: Update swarm-client app references

**File**: `flake.nix`

**Current State**: Lines 245-265 reference individual swarm-client variables for apps

**Action**: Update to use `swarmClientProfiles` attribute set

**Replace lines 245-265**:
```nix
          # Swarm client apps (different profiles)
          swarm-client = {
            type = "app";
            program = "${swarmClient.runner}/bin/swarm-client";
          };
          swarm-client-stress = {
            type = "app";
            program = "${swarmClientStress.runner}/bin/swarm-client";
          };
          swarm-client-gentle = {
            type = "app";
            program = "${swarmClientGentle.runner}/bin/swarm-client";
          };
          swarm-client-burst = {
            type = "app";
            program = "${swarmClientBurst.runner}/bin/swarm-client";
          };
          swarm-client-extreme = {
            type = "app";
            program = "${swarmClientExtreme.runner}/bin/swarm-client";
          };
```

**With**:
```nix
          # Swarm client apps (different profiles)
          swarm-client = {
            type = "app";
            program = "${swarmClientProfiles.default.runner}/bin/swarm-client";
          };
          swarm-client-stress = {
            type = "app";
            program = "${swarmClientProfiles.stress.runner}/bin/swarm-client";
          };
          swarm-client-gentle = {
            type = "app";
            program = "${swarmClientProfiles.gentle.runner}/bin/swarm-client";
          };
          swarm-client-burst = {
            type = "app";
            program = "${swarmClientProfiles.burst.runner}/bin/swarm-client";
          };
          swarm-client-extreme = {
            type = "app";
            program = "${swarmClientProfiles.extreme.runner}/bin/swarm-client";
          };
```

---

#### 1.2.7: Test Step 1.2

**Commands**:
```bash
# Test flake evaluation
nix flake check

# Test that all packages are accessible
nix eval .#packages.x86_64-linux.test-origin
nix eval .#packages.x86_64-linux.test-origin-low-latency
nix eval .#packages.x86_64-linux.test-origin-4k-abr
nix eval .#packages.x86_64-linux.swarm-client
nix eval .#packages.x86_64-linux.swarm-client-stress

# Test that apps are accessible
nix run .#test-origin --help
nix run .#test-origin-low-latency --help
nix run .#swarm-client --help

# Show all packages
nix flake show
```

**Expected**: All commands should succeed, and all profiles should be accessible.

---

### Step 1.3: Split `test-origin/config.nix`

**Goal**: Split the 430-line config file into focused modules for better maintainability.

#### 1.3.1: Create `nix/test-origin/config/` directory

**Action**: Create directory structure:
```bash
mkdir -p nix/test-origin/config
```

---

#### 1.3.2: Create `nix/test-origin/config/profiles.nix`

**File**: `nix/test-origin/config/profiles.nix` (new file)

**Content**: Extract profile definitions from `config.nix` lines 15-177

**Create file with**:
```nix
# Test origin profile definitions
# See: docs/TEST_ORIGIN.md for detailed documentation
{
  # Standard testing profile - balanced latency and safety
  default = {
    hls.segmentDuration = 2;
    hls.listSize = 10;
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    };
    encoder.framerate = 30;
    multibitrate = false;
  };

  # Low-latency profile - 1s segments for fast response
  low-latency = {
    hls.segmentDuration = 1;
    hls.listSize = 6;
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2500k";  # Higher maxrate for bursty encoding
      bufsize = "2000k";  # Smaller buffer for lower latency
      audioBitrate = "96k";
    };
    encoder = {
      framerate = 30;
      preset = "ultrafast";
      tune = "zerolatency";
    };
    multibitrate = false;
  };

  # 4K ABR profile - Multi-bitrate adaptive streaming
  "4k-abr" = {
    hls.segmentDuration = 2;
    hls.listSize = 10;
    multibitrate = true;
    variants = [
      {
        name = "2160p";
        width = 3840;
        height = 2160;
        bitrate = "15000k";
        maxrate = "16500k";
        bufsize = "30000k";
        audioBitrate = "192k";
      }
      {
        name = "1080p";
        width = 1920;
        height = 1080;
        bitrate = "5000k";
        maxrate = "5500k";
        bufsize = "10000k";
        audioBitrate = "128k";
      }
      {
        name = "720p";
        width = 1280;
        height = 720;
        bitrate = "2000k";
        maxrate = "2200k";
        bufsize = "4000k";
        audioBitrate = "128k";
      }
      {
        name = "480p";
        width = 854;
        height = 480;
        bitrate = "1000k";
        maxrate = "1100k";
        bufsize = "2000k";
        audioBitrate = "96k";
      }
      {
        name = "360p";
        width = 640;
        height = 360;
        bitrate = "500k";
        maxrate = "550k";
        bufsize = "1000k";
        audioBitrate = "64k";
      }
    ];
    encoder.framerate = 30;
    # Use first variant for single bitrate settings
    video = {
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    };
  };

  # High-load stress test profile - optimized for stability
  stress-test = {
    hls.segmentDuration = 2;
    hls.listSize = 15;  # Larger window for more stability
    video = {
      width = 1280;
      height = 720;
      bitrate = "1500k";  # Lower bitrate = faster encoding
      maxrate = "1650k";
      bufsize = "3000k";
      audioBitrate = "96k";
    };
    encoder = {
      framerate = 25;  # Lower framerate for less CPU
      preset = "ultrafast";
      tune = "zerolatency";
    };
    multibitrate = false;
  };

  # Logged profile - minimal logging for performance analysis
  # Logs segment requests only with 512k buffer
  logged = {
    logging = {
      enabled = true;
      buffer = "512k";
      flushInterval = "10s";
      gzip = 0;
      segmentsOnly = true;  # Only log .ts requests
    };
  };

  # Debug profile - full logging for debugging
  # Logs all requests with compression
  debug = {
    logging = {
      enabled = true;
      buffer = "256k";
      flushInterval = "5s";
      gzip = 4;
      segmentsOnly = false;  # Log all requests
    };
  };

  # TAP networking profile - high performance
  # Requires: make network-setup (creates hlsbr0 bridge + hlstap0 TAP)
  tap = {
    networking.mode = "tap";
  };

  # TAP + logging combo profile
  tap-logged = {
    networking.mode = "tap";
    logging = {
      enabled = true;
      buffer = "512k";
      flushInterval = "10s";
      gzip = 0;
      segmentsOnly = true;
    };
  };
}
```

---

#### 1.3.3: Create `nix/test-origin/config/base.nix`

**File**: `nix/test-origin/config/base.nix` (new file)

**Content**: Extract base configuration from `config.nix` lines 182-312

**Create file with**:
```nix
# Base configuration for test origin (defaults for all profiles)
{
  # Mode selection
  multibitrate = false;

  # HLS settings
  hls = {
    segmentDuration = 2;
    listSize = 10;
    deleteThreshold = 5;  # INCREASED: Safe buffer for SWR/CDN lag
    # Note: %% is required for systemd unit files (% is a specifier)
    segmentPattern = "seg%%05d.ts";
    playlistName = "stream.m3u8";
    masterPlaylist = "master.m3u8";

    # FFmpeg HLS flags
    flags = [
      "delete_segments"
      "omit_endlist"
      "temp_file"
    ];
  };

  # Server settings
  # See docs/PORTS.md for port documentation
  server = {
    port = 17080;
    hlsDir = "/var/hls";
  };

  # Audio settings
  audio = {
    frequency = 1000;
    sampleRate = 48000;
  };

  # Use smptebars - testsrc2 has issues with -re and duration=0 (produces 0 frames)
  testPattern = "smptebars";

  # Video settings (single bitrate)
  video = {
    width = 1280;
    height = 720;
    bitrate = "2000k";
    maxrate = "2200k";
    bufsize = "4000k";
    audioBitrate = "128k";
  };

  # Default variants (ABR)
  variants = [
    {
      name = "720p";
      width = 1280;
      height = 720;
      bitrate = "2000k";
      maxrate = "2200k";
      bufsize = "4000k";
      audioBitrate = "128k";
    }
    {
      name = "360p";
      width = 640;
      height = 360;
      bitrate = "500k";
      maxrate = "550k";
      bufsize = "1000k";
      audioBitrate = "64k";
    }
  ];

  # Encoder settings
  encoder = {
    framerate = 30;
    preset = "ultrafast";
    tune = "zerolatency";
    profile = "baseline";
    level = "3.1";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # MicroVM networking configuration
  # See: docs/MICROVM_NETWORKING.md
  # ═══════════════════════════════════════════════════════════════════════════
  networking = {
    # Networking mode: "user" (default, zero config) or "tap" (high performance)
    # "user" - QEMU user-mode NAT (~500 Mbps, no host setup)
    # "tap"  - TAP + vhost-net (~10 Gbps, requires make network-setup)
    mode = "user";

    # TAP device configuration (only used when mode = "tap")
    tap = {
      device = "hlstap0";         # TAP device name (created by make network-setup with multi_queue)
      mac = "02:00:00:01:77:01";  # VM MAC address (unique per VM)
    };

    # Static IP for TAP mode (VM needs fixed IP for port forwarding)
    staticIp = "10.177.0.10";
    gateway = "10.177.0.1";
    subnet = "10.177.0.0/24";
  };

  # ═══════════════════════════════════════════════════════════════════════════
  # Logging configuration for performance analysis
  # ═══════════════════════════════════════════════════════════════════════════
  logging = {
    # Enable/disable logging (disabled by default for max performance)
    enabled = false;

    # Log directory
    directory = "/var/log/nginx";

    # Buffer size for reduced I/O (512k recommended for high-load tests)
    buffer = "512k";

    # Flush interval (10s recommended for buffered logging)
    flushInterval = "10s";

    # Gzip compression level (0 = disabled, 1-9 = compression level)
    gzip = 0;

    # Log only segment requests (reduces volume significantly)
    segmentsOnly = false;

    # Log file names
    files = {
      segments = "segments.log";
      manifests = "manifests.log";
      all = "access.log";
    };
  };
}
```

---

#### 1.3.4: Create `nix/test-origin/config/derived.nix`

**File**: `nix/test-origin/config/derived.nix` (new file)

**Content**: Extract derived calculations from `config.nix` lines 330-384

**Create file with**:
```nix
# Derived values computed from merged config
{ config }:

let
  h = config.hls;
  v = config.video;
  enc = config.encoder;
in {
  # GOP size = framerate × segment duration
  gopSize = enc.framerate * h.segmentDuration;

  # Segment lifetime = (listSize + deleteThreshold) × segmentDuration
  segmentLifetimeSec = (h.listSize + h.deleteThreshold) * h.segmentDuration;

  # Playlist window duration
  playlistWindowSec = h.listSize * h.segmentDuration;

  # Parse bitrate string to integer (kbps)
  parseBitrate = str:
    let
      stripped = builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] str;
      num = builtins.fromJSON stripped;
      multiplier = if builtins.match ".*[mM].*" str != null then 1000 else 1;
    in num * multiplier;

  # Total bitrate per variant (video + audio) in kbps
  totalBitrateKbps = (builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] v.bitrate | builtins.fromJSON) +
                     (builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] v.audioBitrate | builtins.fromJSON);

  # Segment size estimate
  segmentSizeKB = (totalBitrateKbps * h.segmentDuration) / 8;

  # Files per variant = listSize + deleteThreshold + 1 (being written)
  filesPerVariant = h.listSize + h.deleteThreshold + 1;

  # Storage per variant
  storagePerVariantMB = (segmentSizeKB * filesPerVariant) / 1024;

  # Number of variants
  variantCount = if config.multibitrate
                 then builtins.length config.variants
                 else 1;

  # Total storage estimate
  totalStorageMB = storagePerVariantMB * variantCount;

  # Recommended tmpfs size: (Bitrate * Window * 2) + 64MB
  # Formula: (total_bitrate_kbps / 8 * playlist_window_sec * 2 * variant_count / 1024) + 64
  recommendedTmpfsMB = let
    bitrateBytes = totalBitrateKbps / 8;  # KB/s
    windowBytes = bitrateBytes * playlistWindowSec;  # KB
    safetyBuffer = windowBytes * 2;  # Double buffer
    perVariant = safetyBuffer * variantCount;
    inMB = perVariant / 1024;
  in builtins.ceil (inMB + 64);
}
```

**Note**: The `parseBitrate` function needs to be used inline in `totalBitrateKbps` since we can't reference `derived.parseBitrate` before `derived` is defined. This is a simplification that works.

**Better approach**: Keep the recursive structure:
```nix
# Derived values computed from merged config
{ config }:

let
  h = config.hls;
  v = config.video;
  enc = config.encoder;

  parseBitrate = str:
    let
      stripped = builtins.replaceStrings ["k" "K" "m" "M"] ["" "" "" ""] str;
      num = builtins.fromJSON stripped;
      multiplier = if builtins.match ".*[mM].*" str != null then 1000 else 1;
    in num * multiplier;

  totalBitrateKbps = parseBitrate v.bitrate + parseBitrate v.audioBitrate;
  segmentSizeKB = (totalBitrateKbps * h.segmentDuration) / 8;
  filesPerVariant = h.listSize + h.deleteThreshold + 1;
  storagePerVariantMB = (segmentSizeKB * filesPerVariant) / 1024;
  variantCount = if config.multibitrate
                 then builtins.length config.variants
                 else 1;
  totalStorageMB = storagePerVariantMB * variantCount;
  playlistWindowSec = h.listSize * h.segmentDuration;
  recommendedTmpfsMB = let
    bitrateBytes = totalBitrateKbps / 8;  # KB/s
    windowBytes = bitrateBytes * playlistWindowSec;  # KB
    safetyBuffer = windowBytes * 2;  # Double buffer
    perVariant = safetyBuffer * variantCount;
    inMB = perVariant / 1024;
  in builtins.ceil (inMB + 64);
in {
  gopSize = enc.framerate * h.segmentDuration;
  segmentLifetimeSec = (h.listSize + h.deleteThreshold) * h.segmentDuration;
  playlistWindowSec = playlistWindowSec;
  parseBitrate = parseBitrate;
  totalBitrateKbps = totalBitrateKbps;
  segmentSizeKB = segmentSizeKB;
  filesPerVariant = filesPerVariant;
  storagePerVariantMB = storagePerVariantMB;
  variantCount = variantCount;
  totalStorageMB = totalStorageMB;
  recommendedTmpfsMB = recommendedTmpfsMB;
}
```

---

#### 1.3.5: Create `nix/test-origin/config/cache.nix`

**File**: `nix/test-origin/config/cache.nix` (new file)

**Content**: Extract cache timing from `config.nix` lines 386-410

**Create file with**:
```nix
# Cache timing configuration (dynamically calculated from segment duration)
{ config }:

let
  h = config.hls;
in {
  # Segments: immutable, cache for full lifetime + safety margin
  segment = {
    maxAge = 60;  # Segments are immutable; generous TTL is safe
    immutable = true;
    public = true;
  };

  # Manifests: TTL = segmentDuration / 2, SWR = segmentDuration
  manifest = {
    maxAge = h.segmentDuration / 2;  # Half segment duration
    staleWhileRevalidate = h.segmentDuration;  # Full segment duration
    public = true;
  };

  # Master playlist: rarely changes
  master = {
    maxAge = 5;
    staleWhileRevalidate = 10;
    public = true;
  };
}
```

---

#### 1.3.6: Update `nix/test-origin/config.nix` to use split modules

**File**: `nix/test-origin/config.nix`

**Action**: Replace entire file content with new structure

**Replace entire file** (lines 1-430) with:
```nix
# Test origin configuration - Function-based with profile support
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Usage:
#   config = import ./config.nix { profile = "default"; lib = lib; }
#   config = import ./config.nix { profile = "low-latency"; lib = lib; }
#   config = import ./config.nix { profile = "4k-abr"; overrides = { hls.listSize = 15; }; lib = lib; }
#
{ profile ? "default", overrides ? {}, lib }:

let
  # Import split modules
  profiles = import ./config/profiles.nix;
  baseConfig = import ./config/base.nix;

  # Merge: base <- profile <- overrides
  profileConfig = profiles.${profile} or (throw "Unknown profile: ${profile}. Available: ${lib.concatStringsSep ", " (builtins.attrNames profiles)}");
  mergedConfig = lib.deepMerge (lib.deepMerge baseConfig profileConfig) overrides;

  # Import derived and cache calculations
  derived = import ./config/derived.nix { config = mergedConfig; };
  cache = import ./config/cache.nix { config = mergedConfig; };

  # Shortcuts for convenience
  h = mergedConfig.hls;
  v = mergedConfig.video;
  enc = mergedConfig.encoder;

in mergedConfig // {
  # Export derived calculations
  inherit derived cache;

  # Computed encoder values
  encoder = mergedConfig.encoder // {
    gopSize = derived.gopSize;
  };

  # Export profile info
  _profile = {
    name = profile;
    availableProfiles = builtins.attrNames profiles;
  };

  # HLS flags string (convenience)
  hlsFlags = lib.concatStringsSep "+" h.flags;
}
```

**Result**: `config.nix` is now ~50 lines instead of 430, with logic split into focused modules.

---

#### 1.3.7: Test Step 1.3

**Commands**:
```bash
# Test that config still loads
nix eval --expr '(import ./nix/test-origin/config.nix { profile = "default"; lib = (import <nixpkgs> {}).lib; })._profile.name'

# Test all profiles
for profile in default low-latency 4k-abr stress-test logged debug tap tap-logged; do
  echo "Testing profile: $profile"
  nix eval --expr "(import ./nix/test-origin/config.nix { profile = \"$profile\"; lib = (import <nixpkgs> {}).lib; })._profile.name"
done

# Test that test-origin still works
nix eval .#packages.x86_64-linux.test-origin
nix run .#test-origin --help
```

**Expected**: All commands should succeed, and all profiles should work identically to before.

---

### Step 1.4: Final Phase 1 Testing

**Commands**:
```bash
# Full flake check
nix flake check

# Test all packages build
nix build .#test-origin
nix build .#test-origin-low-latency
nix build .#test-origin-4k-abr
nix build .#test-origin-container
nix build .#swarm-client
nix build .#swarm-client-stress

# Test all apps run
nix run .#test-origin --help
nix run .#test-origin-low-latency --help
nix run .#swarm-client --help

# Show flake structure
nix flake show
```

**Expected**: All tests pass, functionality unchanged.

---

## Phase 2: Generic Profile System

### Step 2.1: Implement `mkProfileSystem` in `lib.nix`

**Goal**: Create a reusable profile system framework.

#### 2.1.1: Add `mkProfileSystem` to `lib.nix`

**File**: `nix/lib.nix`

**Action**: Add `mkProfileSystem` function after `deepMerge` (around line 32)

**Insert after `deepMerge` function**:
```nix
  # Generic profile system builder
  # Creates a reusable profile framework for components
  #
  # Usage:
  #   profileSystem = lib.mkProfileSystem {
  #     base = { ... };  # Base configuration
  #     profiles = { default = { ... }; low-latency = { ... }; ... };
  #   };
  #   config = profileSystem.getConfig "default" {};
  #
  mkProfileSystem = { base, profiles }:
    rec {
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

**Result**: `lib.nix` now provides a generic profile system.

---

### Step 2.2: Refactor `test-origin/config.nix` to use `mkProfileSystem`

**File**: `nix/test-origin/config.nix`

**Action**: Update to use `mkProfileSystem`

**Replace the entire file** with:
```nix
# Test origin configuration - Function-based with profile support
# See: docs/TEST_ORIGIN.md for detailed documentation
#
# Usage:
#   config = import ./config.nix { profile = "default"; lib = lib; }
#   config = import ./config.nix { profile = "low-latency"; lib = lib; }
#   config = import ./config.nix { profile = "4k-abr"; overrides = { hls.listSize = 15; }; lib = lib; }
#
{ profile ? "default", overrides ? {}, lib }:

let
  # Import split modules
  profiles = import ./config/profiles.nix;
  baseConfig = import ./config/base.nix;

  # Use generic profile system
  profileSystem = lib.mkProfileSystem {
    inherit base profiles;
  };

  # Get merged config
  mergedConfig = profileSystem.getConfig profile overrides;

  # Import derived and cache calculations
  derived = import ./config/derived.nix { config = mergedConfig; };
  cache = import ./config/cache.nix { config = mergedConfig; };

  # Shortcuts for convenience
  h = mergedConfig.hls;
  v = mergedConfig.video;
  enc = mergedConfig.encoder;

in mergedConfig // {
  # Export derived calculations
  inherit derived cache;

  # Computed encoder values
  encoder = mergedConfig.encoder // {
    gopSize = derived.gopSize;
  };

  # Export profile info (already included by getConfig, but explicit for clarity)
  _profile = mergedConfig._profile;

  # HLS flags string (convenience)
  hlsFlags = lib.concatStringsSep "+" h.flags;
}
```

**Result**: `test-origin/config.nix` now uses the generic profile system.

---

### Step 2.3: Refactor `swarm-client/config.nix` to use `mkProfileSystem`

**File**: `nix/swarm-client/config.nix`

**Current State**: Uses simple attribute set merge (line 90: `cfg = base // overrides;`)

**Action**: Update to use `mkProfileSystem` with `lib` parameter

**Change line 8** from:
```nix
{ profile ? "default", overrides ? {} }:
```

**To**:
```nix
{ profile ? "default", overrides ? {}, lib }:
```

**Replace lines 86-124** with:
```nix
  # Use generic profile system
  profileSystem = lib.mkProfileSystem {
    base = {};  # swarm-client has no base config, only profiles
    inherit profiles;
  };

  # Get merged config
  cfg = profileSystem.getConfig profile overrides;

in cfg // {
  # ═══════════════════════════════════════════════════════════════════════════
  # Derived Values
  # ═══════════════════════════════════════════════════════════════════════════
  derived = {
    # Estimated ramp-up duration (seconds)
    rampDuration = cfg.clients / cfg.rampRate;

    # Memory estimate (see docs/MEMORY.md)
    # ~19MB private per process + ~64MB shared/overhead
    estimatedMemoryMB = (cfg.clients * 19) + 64;

    # Recommended file descriptor limit
    # Each FFmpeg needs ~10-15 FDs, plus orchestrator overhead
    recommendedFdLimit = (cfg.clients * 15) + 1000;

    # Recommended ephemeral ports
    # Each FFmpeg client uses 1-4 concurrent connections
    recommendedPorts = cfg.clients * 5;

    # Container memory limit (with 20% headroom)
    containerMemoryMB = builtins.ceil (((cfg.clients * 19) + 64) * 1.2);

    # VM memory (with kernel and service overhead)
    vmMemoryMB = builtins.ceil (((cfg.clients * 19) + 64) * 1.3) + 256;
  };

  # Profile metadata (already included by getConfig, but explicit for clarity)
  _profile = cfg._profile;
}
```

**Action**: Update `swarm-client/default.nix` to pass `lib`

**File**: `nix/swarm-client/default.nix`

**Change line 22** from:
```nix
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
  };
```

**To**:
```nix
  config = import ./config.nix {
    inherit profile;
    overrides = configOverrides;
    lib = lib;
  };
```

---

### Step 2.4: Improve Error Messages

**Goal**: Add better error messages for invalid profiles.

#### 2.4.1: Enhance `mkProfileSystem` error messages

**File**: `nix/lib.nix`

**Action**: Update `getConfig` function to have better error message

**Replace the `getConfig` function** (in `mkProfileSystem`) with:
```nix
      # Get config for a profile with optional overrides
      getConfig = profile: overrides:
        let
          available = builtins.attrNames profiles;
          profileConfig = profiles.${profile} or (
            throw ''
              Unknown profile: ${profile}

              Available profiles:
              ${lib.concatMapStringsSep "\n" (p: "  - ${p}") available}
            ''
          );
          merged = deepMerge (deepMerge base profileConfig) overrides;
        in merged // {
          _profile = {
            name = profile;
            availableProfiles = available;
          };
        };
```

**Also update `validateProfile`**:
```nix
      # Validate profile exists
      validateProfile = profile:
        let
          available = builtins.attrNames profiles;
        in
        if builtins.hasAttr profile profiles
        then true
        else throw ''
          Unknown profile: ${profile}

          Available profiles:
          ${lib.concatMapStringsSep "\n" (p: "  - ${p}") available}
        '';
```

---

### Step 2.5: Test Phase 2

**Commands**:
```bash
# Test profile system
nix eval --expr '(import ./nix/lib.nix { pkgs = import <nixpkgs> {}; lib = (import <nixpkgs> {}).lib; }).mkProfileSystem { base = {}; profiles = { default = { a = 1; }; }; }.getConfig "default" {}'

# Test invalid profile error
nix eval --expr '(import ./nix/lib.nix { pkgs = import <nixpkgs> {}; lib = (import <nixpkgs> {}).lib; }).mkProfileSystem { base = {}; profiles = { default = {}; }; }.getConfig "invalid" {}' 2>&1 || true

# Test test-origin with profile system
nix eval .#packages.x86_64-linux.test-origin
nix eval .#packages.x86_64-linux.test-origin-low-latency

# Test swarm-client with profile system
nix eval .#packages.x86_64-linux.swarm-client
nix eval .#packages.x86_64-linux.swarm-client-stress

# Full flake check
nix flake check

# Test all profiles
for profile in default low-latency 4k-abr stress-test logged debug tap tap-logged; do
  echo "Testing test-origin profile: $profile"
  nix eval --expr "(import ./nix/test-origin/config.nix { profile = \"$profile\"; lib = (import <nixpkgs> {}).lib; })._profile.name"
done

for profile in default stress gentle burst extreme; do
  echo "Testing swarm-client profile: $profile"
  nix eval --expr "(import ./nix/swarm-client/config.nix { profile = \"$profile\"; lib = (import <nixpkgs> {}).lib; })._profile.name"
done
```

**Expected**: All commands succeed, error messages are more helpful.

---

## Testing Checklist

### Phase 1 Testing

- [ ] `nix flake check` passes
- [ ] All test-origin profiles accessible: `nix eval .#packages.x86_64-linux.test-origin-*`
- [ ] All swarm-client profiles accessible: `nix eval .#packages.x86_64-linux.swarm-client-*`
- [ ] All apps run: `nix run .#test-origin --help`, etc.
- [ ] Config files load correctly
- [ ] `deepMerge` works from `lib.nix`
- [ ] Split config modules work

### Phase 2 Testing

- [ ] `mkProfileSystem` works
- [ ] `test-origin/config.nix` uses profile system
- [ ] `swarm-client/config.nix` uses profile system
- [ ] Error messages are improved
- [ ] All profiles still work
- [ ] Overrides still work
- [ ] `nix flake check` passes
- [ ] Integration test passes (if applicable)

### Regression Testing

- [ ] Build all packages: `nix build .#test-origin`, etc.
- [ ] Run all apps: `nix run .#test-origin`, etc.
- [ ] Compare outputs before/after (if possible)
- [ ] Test with actual usage (run test-origin, swarm-client)

---

## Rollback Plan

If issues arise, rollback steps:

### Phase 1 Rollback

1. **Revert `lib.nix`**: Remove `deepMerge` function
2. **Revert `test-origin/config.nix`**: Restore local `deepMerge`, remove `lib` parameter
3. **Revert `test-origin/default.nix`**: Remove `lib` parameter from config import
4. **Revert `flake.nix`**: Restore manual profile instantiation
5. **Remove split config files**: Delete `nix/test-origin/config/` directory, restore original `config.nix`

### Phase 2 Rollback

1. **Revert `lib.nix`**: Remove `mkProfileSystem` function
2. **Revert `test-origin/config.nix`**: Restore manual merge logic
3. **Revert `swarm-client/config.nix`**: Restore simple `//` merge
4. **Revert `swarm-client/default.nix`**: Remove `lib` parameter

### Git Strategy

**Recommended**: Create a branch for refactoring:
```bash
git checkout -b nix-refactor-phase1
# Make Phase 1 changes
git commit -m "Phase 1: Extract deepMerge, reduce flake.nix repetition, split config"
# Test thoroughly
# If good, merge or continue to Phase 2

git checkout -b nix-refactor-phase2
# Make Phase 2 changes
git commit -m "Phase 2: Generic profile system"
# Test thoroughly
```

This allows easy rollback with `git checkout main` if needed.

---

## Summary

This implementation plan provides:

1. **Exact file paths and line numbers** for all changes
2. **Step-by-step instructions** with before/after code
3. **Testing commands** for each step
4. **Rollback procedures** if issues arise

**Estimated Time**:
- Phase 1: 2-4 hours (including testing)
- Phase 2: 4-8 hours (including testing)

**Risk**: Low (incremental changes, comprehensive testing)

**Next Steps**: Review this plan, then proceed with Phase 1 implementation.
