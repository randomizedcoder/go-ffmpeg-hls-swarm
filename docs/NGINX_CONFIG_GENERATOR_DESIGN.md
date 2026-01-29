# Nginx Config Generator Design

> **Status**: Design Proposal
> **Related**: [TEST_ORIGIN.md](TEST_ORIGIN.md), [NIX_NGINX_REFERENCE.md](NIX_NGINX_REFERENCE.md)

---

## Overview

Provide a way for users to easily view and export the nginx configuration that will be used by the test origin server. This helps with:
- **Debugging**: Understanding what nginx config is actually being used
- **Customization**: Seeing the generated config before modifying it
- **Documentation**: Sharing config examples for different profiles
- **Validation**: Verifying cache headers and performance settings

---

## Design Goals

1. **Easy Access**: Simple command to view config
2. **Profile Support**: View config for any profile (default, low-latency, stress, etc.)
3. **Multiple Formats**: Both file output and stdout viewing
4. **Consistent Interface**: Follows existing Nix package/app patterns

---

## Proposed Interface

### Option 1: Package + App (Recommended)

**Packages** (for file output):
```bash
# Default profile
nix build .#test-origin-nginx-config
cat ./result

# Specific profile
nix build .#test-origin-nginx-config-low-latency
nix build .#test-origin-nginx-config-stress
nix build .#test-origin-nginx-config-4k-abr
```

**App** (for quick viewing):
```bash
# Default profile
nix run .#nginx-config

# Specific profile
nix run .#nginx-config low-latency
nix run .#nginx-config stress
nix run .#nginx-config 4k-abr
```

### Option 2: App Only (Simpler)

**App** (with profile argument):
```bash
# Default profile
nix run .#nginx-config

# Specific profile
nix run .#nginx-config -- low-latency
nix run .#nginx-config -- stress
```

---

## Implementation Plan

### 1. Nix Package Structure

Create packages in `flake.nix`:

```nix
# In packages section
test-origin-nginx-config = testOriginProfiles.default.nginxConfig;
test-origin-nginx-config-low-latency = testOriginProfiles.low-latency.nginxConfig;
test-origin-nginx-config-stress = testOriginProfiles.stress-test.nginxConfig;
test-origin-nginx-config-4k-abr = testOriginProfiles."4k-abr".nginxConfig;
# ... other profiles
```

### 2. Export from nginx.nix

Modify `nix/test-origin/nginx.nix` to export the config file as a package:

```nix
# Add to the return value
in rec {
  # Existing exports...
  inherit segmentCacheControl manifestCacheControl masterCacheControl;
  inherit logFormats segmentAccessLog manifestAccessLog defaultAccessLog;

  # New: Export config file as a package
  configPackage = configFile;  # Already a derivation (writeText)
}
```

### 3. Export from default.nix

Modify `nix/test-origin/default.nix` to expose nginx config:

```nix
# In the profile return value
{
  # Existing exports...
  runner = ...;
  container = ...;
  microvm = ...;

  # New: Nginx config package
  nginxConfig = nginx.configPackage;
}
```

### 4. App Implementation

Create app in `flake.nix`:

```nix
apps = {
  # ... existing apps

  nginx-config = {
    type = "app";
    program = "${pkgs.writeShellScript "nginx-config" ''
      set -euo pipefail

      PROFILE="''${1:-default}"

      # Build and output the config
      nix build ".#test-origin-nginx-config-$PROFILE" --print-out-paths | \
        xargs cat
    ''}";
  };
};
```

### 5. Alternative: Direct Access

For even simpler access, we could add a script that reads from the built config:

```nix
apps.nginx-config = {
  type = "app";
  program = pkgs.writeShellScript "nginx-config" ''
    set -euo pipefail

    PROFILE="''${1:-default}"

    # Use nix eval to get the config directly
    nix eval --raw ".#test-origin-nginx-config-$PROFILE" 2>/dev/null || \
      nix build ".#test-origin-nginx-config-$PROFILE" --print-out-paths | xargs cat
  '';
};
```

---

## File Structure

```
nix/test-origin/
├── default.nix          # Exports nginxConfig from profile
├── nginx.nix            # Exports configPackage
└── ...
```

---

## Usage Examples

### View Default Config
```bash
# Quick view
nix run .#nginx-config

# Save to file
nix build .#test-origin-nginx-config
cp ./result nginx-default.conf
```

### View Profile-Specific Config
```bash
# Low-latency profile
nix run .#nginx-config low-latency

# Stress test profile
nix run .#nginx-config stress

# Save to file
nix build .#test-origin-nginx-config-stress
cp ./result nginx-stress.conf
```

### Compare Configs
```bash
# Compare default vs low-latency
diff <(nix run .#nginx-config) <(nix run .#nginx-config low-latency)
```

### Use in Documentation
```bash
# Generate config for docs
nix build .#test-origin-nginx-config
cat ./result > docs/examples/nginx-default.conf
```

---

## Design Decisions

### Why Package + App?

- **Package**: Useful for CI/CD, file operations, scripting
- **App**: Convenient for quick viewing, follows existing patterns

### Why Profile-Specific Packages?

- **Explicit**: Clear what profile you're getting
- **Cacheable**: Nix can cache each profile separately
- **Discoverable**: `nix flake show` lists all available configs

### Alternative: Single Package with Profile Argument

Could use a single package with profile as argument:
```bash
nix build .#test-origin-nginx-config --argstr profile low-latency
```

**Pros**: Fewer package definitions
**Cons**: Less discoverable, harder to use

---

## Implementation Notes

1. **Config File Location**: The config is already generated in `nginx.nix` as `configFile`
2. **Profile Access**: Profiles are already structured in `nix/test-origin/config/profiles.nix`
3. **Minimal Changes**: Only need to export existing config file as package
4. **Backward Compatible**: Doesn't change existing functionality

---

## Testing

After implementation, verify:

```bash
# 1. Package builds
nix build .#test-origin-nginx-config

# 2. Config is valid nginx syntax
nginx -t -c $(nix build --print-out-paths .#test-origin-nginx-config)

# 3. App works
nix run .#nginx-config | head -20

# 4. Profile-specific works
nix run .#nginx-config low-latency | grep -q "low-latency" || echo "Profile-specific config"
```

---

## Future Enhancements

1. **Syntax Highlighting**: Pipe output through `bat` or `pygmentize` for colored output
2. **Diff Tool**: Compare configs between profiles
3. **Validation**: Verify config syntax before output
4. **Documentation Comments**: Add inline comments explaining settings
5. **Export Formats**: JSON/YAML representation of config structure

---

## Questions for Review

1. **Package naming**: `test-origin-nginx-config` vs `nginx-config-test-origin`?
2. **App naming**: `nginx-config` vs `show-nginx-config` vs `test-origin-nginx-config`?
3. **Profile argument**: Positional vs `--profile` flag?
4. **Output format**: Raw config vs formatted/annotated?

---

## Recommendation

**Preferred Approach**: Option 1 (Package + App)

- **Packages**: `test-origin-nginx-config[-<profile>]` for file operations
- **App**: `nginx-config [profile]` for quick viewing
- **Profile argument**: Positional (simpler, matches existing patterns)

This provides maximum flexibility while keeping the interface simple and discoverable.
