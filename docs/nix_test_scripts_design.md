# Nix Test Scripts Design

> **Type**: Design Document
> **Status**: Draft
> **Related**: [nix_refactor_implementation_plan.md](nix_refactor_implementation_plan.md), [nix_refactor_implementation_log.md](nix_refactor_implementation_log.md)

This document describes the design for automated testing scripts to verify all Nix flake outputs after refactoring.

---

## Table of Contents

- [Overview](#overview)
- [Goals](#goals)
- [Script Structure](#script-structure)
- [Test Categories](#test-categories)
- [Script Implementation](#script-implementation)
- [Usage](#usage)
- [Output and Reporting](#output-and-reporting)

---

## Overview

After the Nix refactoring (Phase 1 and Phase 2), we need comprehensive testing to ensure:
- All packages build successfully
- All profiles are accessible
- All apps work correctly
- Containers can be built
- MicroVMs can be built (Linux only)
- Backward compatibility is maintained

Rather than manually running `nix build` and `nix eval` commands, we'll create automated test scripts that:
- Run all tests systematically
- Provide clear pass/fail reporting
- Can be run in CI/CD
- Are fast (parallel where possible)
- Are maintainable

---

## Goals

### Primary Goals

1. **Verify all packages build** - Ensure every package derivation builds successfully
2. **Verify all profiles work** - Test that all profile variants are accessible and functional
3. **Verify containers build** - Test OCI container image builds
4. **Verify MicroVMs build** - Test MicroVM builds (Linux only, requires KVM)
5. **Verify apps work** - Test that all apps can be executed (at least show help/version)
6. **Fast execution** - Use parallel builds where possible
7. **Clear reporting** - Show what passed, what failed, and why

### Secondary Goals

8. **CI/CD integration** - Scripts should work in CI environments
9. **Selective testing** - Ability to test specific categories
10. **Dry-run mode** - Show what would be tested without running
11. **Performance metrics** - Track build times

---

## Script Structure

### Directory Layout

```
scripts/
├── nix-tests/
│   ├── lib.sh                    # Common functions and utilities
│   ├── shellcheck.sh             # Run shellcheck on all test scripts
│   ├── test-packages.sh         # Test all package builds
│   ├── test-profiles.sh         # Test all profile accessibility
│   ├── test-containers.sh       # Test container builds
│   ├── test-microvms.sh         # Test MicroVM builds (Linux only)
│   ├── test-apps.sh             # Test app execution
│   └── test-all.sh              # Run all tests
└── README.md                    # Documentation (update existing)
```

### Script Organization

**`lib.sh`** - Shared utilities:
- Color output functions (green/red/yellow)
- Logging functions
- Error handling
- Parallel execution helpers
- Result tracking

**Individual test scripts** - Focused on specific test categories:
- Can be run independently
- Use `lib.sh` for common functionality
- Return exit codes for CI integration

**`test-all.sh`** - Master script:
- Runs all test categories
- Aggregates results
- Provides summary report

---

## Test Categories

### 1. Package Build Tests

**Script**: `test-packages.sh`

**Tests**:
- Core package: `go-ffmpeg-hls-swarm`
- All test-origin profiles (8 profiles):
  - `test-origin` (default)
  - `test-origin-low-latency`
  - `test-origin-4k-abr`
  - `test-origin-stress`
  - `test-origin-logged`
  - `test-origin-debug`
  - `test-origin-tap` (if applicable)
  - `test-origin-tap-logged` (if applicable)
- All swarm-client profiles (5 profiles):
  - `swarm-client` (default)
  - `swarm-client-stress`
  - `swarm-client-gentle`
  - `swarm-client-burst`
  - `swarm-client-extreme`

**Method**: `nix build .#packages.<system>.<package-name>`

**Parallelization**: Build all packages in parallel using `nix build` with multiple targets

**Expected Time**: ~5-10 minutes (depending on cache hits)

---

### 2. Profile Accessibility Tests

**Script**: `test-profiles.sh`

**Tests**:
- Verify all profiles can be evaluated
- Verify profile metadata is correct
- Verify profile names match expected list
- Test profile overrides work

**Method**: `nix eval .#packages.<system>.<package-name> --apply 'x: x.outPath'`

**Parallelization**: Evaluate all profiles in parallel

**Expected Time**: ~30 seconds

---

### 3. Container Build Tests

**Script**: `test-containers.sh`

**Tests**:
- `test-origin-container` builds successfully
- `swarm-client-container` builds successfully
- Container images are valid OCI images
- Container images have expected structure

**Method**:
- `nix build .#packages.<system>.<container-name>`
- Verify output is a valid container image (check for `manifest.json`, etc.)

**Parallelization**: Build both containers in parallel

**Expected Time**: ~3-5 minutes

---

### 4. MicroVM Build Tests

**Script**: `test-microvms.sh`

**Tests**:
- All test-origin MicroVM variants build (Linux only):
  - `test-origin-vm`
  - `test-origin-vm-low-latency`
  - `test-origin-vm-stress`
  - `test-origin-vm-logged`
  - `test-origin-vm-debug`
  - `test-origin-vm-tap`
  - `test-origin-vm-tap-logged`
- Verify MicroVM outputs are valid
- Skip on non-Linux systems with clear message

**Method**:
- `nix build .#packages.<system>.<microvm-name>`
- Check for MicroVM-specific outputs

**Parallelization**: Build all MicroVMs in parallel

**Expected Time**: ~10-15 minutes (MicroVMs are large)

**Note**: Requires KVM support. Script should detect and skip gracefully on systems without KVM.

---

### 5. App Execution Tests

**Script**: `test-apps.sh`

**Tests**:
- All apps can be executed
- Apps show help or version (don't need full execution)
- Test-origin apps (all profiles)
- Swarm-client apps (all profiles)
- Core apps (welcome, build, run)

**Method**:
- `nix run .#<app-name> -- --help` or `--version`
- Verify exit code is 0
- Verify output contains expected text

**Parallelization**: Run apps sequentially (they're fast)

**Expected Time**: ~1-2 minutes

---

## Script Implementation

### Shellcheck Compliance

**All scripts must pass `shellcheck` without errors or warnings.**

**Requirements**:
- Use `#!/usr/bin/env bash` shebang
- Set `set -euo pipefail` for strict error handling
- Quote all variables: `"$var"` not `$var`
- Use `$(...)` for command substitution, not backticks
- Quote array expansions: `"${array[@]}"`
- Use `[[ ]]` for conditionals (bash-specific)
- Avoid `eval` and other unsafe constructs
- Declare functions with `function_name() { ... }` syntax

**Testing**:
- Run `shellcheck scripts/nix-tests/*.sh` before committing
- Add shellcheck to CI/CD pipeline
- Fix all warnings, not just errors

**Example shellcheck-compliant code**:
```bash
#!/usr/bin/env bash
set -euo pipefail

# Good: Quoted variables
name="test"
echo "$name"

# Good: Quoted array expansion
files=("file1" "file2")
for file in "${files[@]}"; do
    echo "$file"
done

# Good: Using [[ ]] for conditionals
if [[ -f "$file" ]]; then
    echo "File exists"
fi
```

### Shellcheck Validation Script (`shellcheck.sh`)

**Purpose**: Ensure all test scripts pass shellcheck validation

```bash
#!/usr/bin/env bash
# Run shellcheck on all Nix test scripts

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR

# Colors for output
readonly RED='\033[0;31m'
readonly GREEN='\033[0;32m'
readonly YELLOW='\033[1;33m'
readonly NC='\033[0m' # No Color

log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

# Check if shellcheck is available
if ! command -v shellcheck >/dev/null 2>&1; then
    log_error "shellcheck is not installed"
    log_info "Install with: nix-shell -p shellcheck"
    log_info "Or: brew install shellcheck (macOS)"
    exit 1
fi

log_info "Running shellcheck on Nix test scripts..."
echo ""

FAILED=0
PASSED=0

# Find all shell scripts in nix-tests directory
readonly SCRIPTS=(
    "$SCRIPT_DIR/lib.sh"
    "$SCRIPT_DIR/test-packages.sh"
    "$SCRIPT_DIR/test-profiles.sh"
    "$SCRIPT_DIR/test-containers.sh"
    "$SCRIPT_DIR/test-microvms.sh"
    "$SCRIPT_DIR/test-apps.sh"
    "$SCRIPT_DIR/test-all.sh"
    "$SCRIPT_DIR/shellcheck.sh"
)

FAILED_FILES=()

for script in "${SCRIPTS[@]}"; do
    if [[ ! -f "$script" ]]; then
        log_warn "Skipping $script (not found)"
        continue
    fi

    if shellcheck -x "$script"; then
        log_info "✓ $(basename "$script")"
        ((PASSED++))
    else
        log_error "✗ $(basename "$script")"
        FAILED_FILES+=("$script")
        ((FAILED++))
    fi
done

echo ""
echo "════════════════════════════════════════════════════════════"
echo "Shellcheck Summary"
echo "════════════════════════════════════════════════════════════"
echo "Passed:  $PASSED"
echo "Failed:  $FAILED"
echo ""

if [[ $FAILED -gt 0 ]]; then
    log_error "Some scripts failed shellcheck validation:"
    for file in "${FAILED_FILES[@]}"; do
        echo "  - $file"
    done
    echo ""
    log_info "Run 'shellcheck -x <file>' for details"
    exit 1
fi

log_info "All scripts passed shellcheck validation!"
exit 0
```

**Usage**:
```bash
# Run shellcheck validation
./scripts/nix-tests/shellcheck.sh

# Or via Makefile
make shellcheck-nix-tests
```

### Common Functions (`lib.sh`)

```bash
#!/usr/bin/env bash
# Common functions for Nix test scripts

set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test results tracking
PASSED=0
FAILED=0
SKIPPED=0
RESULTS=()

# Logging functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $*"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $*"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $*"
}

log_test() {
    echo -e "[TEST] $*"
}

# Test result tracking
test_pass() {
    ((PASSED++))
    RESULTS+=("PASS: $1")
    log_info "✓ $1"
}

test_fail() {
    ((FAILED++))
    RESULTS+=("FAIL: $1 - $2")
    log_error "✗ $1: $2"
}

test_skip() {
    ((SKIPPED++))
    RESULTS+=("SKIP: $1 - $2")
    log_warn "⊘ $1: $2"
}

# Note: Shellcheck may warn about arithmetic on unset variables.
# We initialize PASSED, FAILED, SKIPPED to 0, so this is safe.
# shellcheck disable=SC2034

# Print summary
print_summary() {
    echo ""
    echo "════════════════════════════════════════════════════════════"
    echo "Test Summary"
    echo "════════════════════════════════════════════════════════════"
    echo "Passed:  $PASSED"
    echo "Failed:  $FAILED"
    echo "Skipped: $SKIPPED"
    echo ""

    if [[ $FAILED -gt 0 ]]; then
        echo "Failed tests:"
        for result in "${RESULTS[@]}"; do
            if [[ "$result" == FAIL:* ]]; then
                echo "  $result"
            fi
        done
        return 1
    fi

    return 0
}

# Get system from nix
get_system() {
    nix eval --impure --expr 'builtins.currentSystem' --raw
}

# Check if running on Linux
is_linux() {
    [[ "$(uname)" == "Linux" ]]
}

# Check if KVM is available (for MicroVM tests)
has_kvm() {
    if is_linux && [[ -e /dev/kvm ]] && [[ -r /dev/kvm ]] && [[ -w /dev/kvm ]]; then
        return 0
    else
        return 1
    fi
}
```

### Example: `test-packages.sh` (Shellcheck-compliant)

```bash
#!/usr/bin/env bash
# Test all package builds

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

SYSTEM=$(get_system)

log_info "Testing package builds for $SYSTEM"
log_info "This may take several minutes..."

# Core package
log_test "Building go-ffmpeg-hls-swarm..."
if nix build ".#packages.$SYSTEM.go-ffmpeg-hls-swarm" --no-link 2>&1; then
    test_pass "go-ffmpeg-hls-swarm"
else
    test_fail "go-ffmpeg-hls-swarm" "Build failed"
fi

# Test-origin profiles
readonly PROFILES=(
    "test-origin"
    "test-origin-low-latency"
    "test-origin-4k-abr"
    "test-origin-stress"
    "test-origin-logged"
    "test-origin-debug"
)

for profile in "${PROFILES[@]}"; do
    log_test "Building $profile..."
    if nix build ".#packages.$SYSTEM.$profile" --no-link 2>&1; then
        test_pass "$profile"
    else
        test_fail "$profile" "Build failed"
    fi
done

# Swarm-client profiles
readonly CLIENT_PROFILES=(
    "swarm-client"
    "swarm-client-stress"
    "swarm-client-gentle"
    "swarm-client-burst"
    "swarm-client-extreme"
)

for profile in "${CLIENT_PROFILES[@]}"; do
    log_test "Building $profile..."
    if nix build ".#packages.$SYSTEM.$profile" --no-link 2>&1; then
        test_pass "$profile"
    else
        test_fail "$profile" "Build failed"
    fi
done

print_summary
```

### Example: `test-all.sh` (Shellcheck-compliant)

```bash
#!/usr/bin/env bash
# Run all Nix tests

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

readonly START_TIME=$(date +%s)

log_info "════════════════════════════════════════════════════════════"
log_info "Running All Nix Tests"
log_info "════════════════════════════════════════════════════════════"
echo ""

# Run test categories
"$SCRIPT_DIR/test-profiles.sh" || true
echo ""
"$SCRIPT_DIR/test-packages.sh" || true
echo ""
"$SCRIPT_DIR/test-containers.sh" || true
echo ""

if is_linux && has_kvm; then
    "$SCRIPT_DIR/test-microvms.sh" || true
    echo ""
else
    log_warn "Skipping MicroVM tests (not on Linux or KVM not available)"
    echo ""
fi

"$SCRIPT_DIR/test-apps.sh" || true
echo ""

readonly END_TIME=$(date +%s)
readonly DURATION=$((END_TIME - START_TIME))

log_info "════════════════════════════════════════════════════════════"
log_info "All Tests Completed in ${DURATION}s"
log_info "════════════════════════════════════════════════════════════"

print_summary
exit $?
```

---

## Usage

### Running Shellcheck Validation

```bash
# Run shellcheck on all test scripts
./scripts/nix-tests/shellcheck.sh

# Or via Makefile (recommended)
make shellcheck-nix-tests
```

### Running Individual Test Scripts

```bash
# Test only package builds
./scripts/nix-tests/test-packages.sh

# Test only containers
./scripts/nix-tests/test-containers.sh

# Test only MicroVMs (Linux only)
./scripts/nix-tests/test-microvms.sh
```

### Running All Tests

```bash
# Run complete test suite
./scripts/nix-tests/test-all.sh

# Or via Makefile (when implemented)
make test-nix-all
```

### CI/CD Integration

```yaml
# Example GitHub Actions
- name: Shellcheck Nix Test Scripts
  run: make shellcheck-nix-tests

- name: Test Nix Packages
  run: ./scripts/nix-tests/test-packages.sh

- name: Test Nix Containers
  run: ./scripts/nix-tests/test-containers.sh

- name: Test Nix MicroVMs (Linux only)
  if: runner.os == 'Linux'
  run: ./scripts/nix-tests/test-microvms.sh
```

---

## Output and Reporting

### Console Output

Scripts should provide:
- Clear test names
- Pass/fail indicators (✓/✗)
- Error messages for failures
- Progress indicators for long-running tests
- Summary at the end

### Example Output

```
[INFO] Testing package builds for x86_64-linux
[INFO] This may take several minutes...

[TEST] Building go-ffmpeg-hls-swarm...
[INFO] ✓ go-ffmpeg-hls-swarm

[TEST] Building test-origin...
[INFO] ✓ test-origin

[TEST] Building test-origin-low-latency...
[INFO] ✓ test-origin-low-latency

...

════════════════════════════════════════════════════════════
Test Summary
════════════════════════════════════════════════════════════
Passed:  14
Failed:  0
Skipped:  0
```

### Exit Codes

- `0` - All tests passed
- `1` - One or more tests failed
- Scripts should continue running even if individual tests fail (use `|| true`)

### Log Files (Optional)

For CI/CD, scripts could optionally write results to a file:
- `nix-test-results.json` - Machine-readable results
- `nix-test-results.txt` - Human-readable summary

---

## Implementation Plan

### Phase 1: Core Infrastructure

1. Create `scripts/nix-tests/` directory
2. Implement `lib.sh` with common functions
3. Create `shellcheck.sh` script to validate all scripts
4. Add Makefile target for shellcheck
5. Create basic test script template
6. **Run shellcheck on all scripts and fix any issues**

### Phase 2: Individual Test Scripts

1. `test-profiles.sh` - Profile accessibility (fastest, start here)
2. `test-packages.sh` - Package builds
3. `test-containers.sh` - Container builds
4. `test-apps.sh` - App execution
5. `test-microvms.sh` - MicroVM builds (Linux only)

### Phase 3: Integration

1. `test-all.sh` - Master script
2. Update `scripts/README.md` with documentation
3. Add scripts to `.gitignore` if needed (probably not)

### Phase 4: Enhancement (Optional)

1. Parallel execution for faster tests
2. JSON output for CI/CD
3. Performance metrics
4. Dry-run mode

### Phase 5: Quality Assurance

1. **Run shellcheck on all scripts**: `shellcheck scripts/nix-tests/*.sh`
2. **Fix all warnings and errors**
3. **Add shellcheck to CI/CD pipeline**
4. **Document shellcheck exceptions** (if any, with `# shellcheck disable=SC####`)

---

## Testing the Test Scripts

Before using the scripts to verify the refactoring, we should:

1. **Run shellcheck** - Ensure all scripts pass: `shellcheck scripts/nix-tests/*.sh`
2. **Test on a clean system** - Ensure scripts work without assumptions
3. **Test error handling** - Verify scripts handle failures gracefully
4. **Test on different systems** - Linux and macOS if possible
5. **Verify output clarity** - Ensure results are easy to understand

---

## Future Enhancements

### Performance Optimization

- Use `nix build` with multiple targets for parallel builds
- Cache test results
- Skip tests that haven't changed

### Advanced Features

- Compare build times before/after refactoring
- Test profile override functionality
- Verify derived values are correct
- Test error messages for invalid profiles

### CI/CD Integration

- GitHub Actions workflow
- GitLab CI configuration
- Automated testing on PRs
- **Shellcheck validation** in CI pipeline

---

## Questions for Review

1. **Script location**: Is `scripts/nix-tests/` the right place, or should they be in `scripts/` directly?

2. **Parallel execution**: Should we use `nix build` with multiple targets, or run builds sequentially for clearer output?

3. **MicroVM testing**: Should we skip MicroVM tests by default and require a flag, or auto-detect?

4. **Error handling**: Should scripts stop on first failure, or continue and report all failures?

5. **Output format**: Do we need JSON output for CI/CD, or is console output sufficient?

6. **Performance**: Should we track and report build times?

7. **Shellcheck**: Are there any specific shellcheck rules we should disable, or should we aim for zero warnings?

---

## Makefile Integration

### New Makefile Targets

Add the following targets to the existing `Makefile`:

```makefile
# ============================================================================
# Nix Test Scripts
# ============================================================================

.PHONY: shellcheck-nix-tests test-nix-all test-nix-packages test-nix-profiles test-nix-containers test-nix-microvms test-nix-apps

shellcheck-nix-tests: ## Run shellcheck on all Nix test scripts
	@./scripts/nix-tests/shellcheck.sh

test-nix-all: shellcheck-nix-tests ## Run all Nix tests (packages, profiles, containers, apps, microvms)
	@./scripts/nix-tests/test-all.sh

test-nix-packages: ## Test all Nix package builds
	@./scripts/nix-tests/test-packages.sh

test-nix-profiles: ## Test all Nix profile accessibility
	@./scripts/nix-tests/test-profiles.sh

test-nix-containers: ## Test all Nix container builds
	@./scripts/nix-tests/test-containers.sh

test-nix-microvms: ## Test all Nix MicroVM builds (Linux only, requires KVM)
	@./scripts/nix-tests/test-microvms.sh

test-nix-apps: ## Test all Nix app execution
	@./scripts/nix-tests/test-apps.sh
```

**Integration with existing targets**:
- Add `shellcheck-nix-tests` as a dependency to `check` target
- Add `test-nix-all` to CI pipeline
- Include in `ci` target for local CI simulation

**Updated `check` target**:
```makefile
check: check-go check-nix shellcheck-nix-tests ## Run all checks (including shellcheck)
```

**Updated `ci` target**:
```makefile
ci: check-nix fmt-nix lint test-unit shellcheck-nix-tests ## Run CI pipeline locally
	@echo "$(GREEN)CI checks passed$(RESET)"
```

---

## Conclusion

This design provides a comprehensive testing framework for verifying the Nix refactoring. The scripts will:

- Test all packages, profiles, containers, and MicroVMs
- Provide clear pass/fail reporting
- Work in both local and CI/CD environments
- Be maintainable and extensible
- **Enforce shellcheck compliance via automated script and Makefile target**

The `shellcheck.sh` script and Makefile target ensure that:
- Shellcheck is always run before committing
- CI/CD can easily validate script quality
- No shellcheck violations can be accidentally introduced
- Developers have a simple command (`make shellcheck-nix-tests`) to check scripts

Once approved, we can implement these scripts and use them to complete the testing phase of the refactoring.
