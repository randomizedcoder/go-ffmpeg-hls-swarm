# Security Considerations

> **Type**: Contributor Documentation
> **Related**: [DESIGN.md](DESIGN.md), [CONFIGURATION.md](CONFIGURATION.md), [NGINX_SECURITY.md](NGINX_SECURITY.md), [HLS_GENERATOR_SECURITY.md](HLS_GENERATOR_SECURITY.md)

This document covers security considerations for `go-ffmpeg-hls-swarm`, with particular focus on the TLS verification bypass feature (`--dangerous` flag).

### MicroVM Service Hardening

| Service | Document | Starting Score | Target Score |
|---------|----------|----------------|--------------|
| **nginx** | [NGINX_SECURITY.md](NGINX_SECURITY.md) | 1.6 | **1.1** ✅ |
| **hls-generator** | [HLS_GENERATOR_SECURITY.md](HLS_GENERATOR_SECURITY.md) | 8.3 | ~1.5-2.0 |

Both documents cover systemd isolation, syscall filtering, capability restrictions, and other hardening techniques.

---

## Table of Contents

- [1. General Security](#1-general-security)
- [2. TLS Verification and `--dangerous`](#2-tls-verification-and---dangerous)
  - [2.1 Why TLS Verification Must Be Disabled](#21-why-tls-verification-must-be-disabled)
  - [2.2 Security Implications](#22-security-implications)
  - [2.3 The `--dangerous` Safety Gate](#23-the---dangerous-safety-gate)
  - [2.4 Runtime Warnings](#24-runtime-warnings)
  - [2.5 When It's Acceptable](#25-when-its-acceptable)
  - [2.6 When NOT to Use](#26-when-not-to-use)
- [3. SNI/TLS Limitations](#3-snitls-limitations)
- [4. Implementation](#4-implementation)

---

## 1. General Security

| Consideration | Details |
|---------------|---------|
| **No sensitive data in logs** | If auth headers added later, redact in logs |
| **No shell injection** | Process args constructed safely (no shell expansion) |
| **Metrics exposed on all interfaces** | Be aware `0.0.0.0:9090` is accessible remotely |
| **FFmpeg subprocess isolation** | Each FFmpeg runs in its own process group |

---

## 2. TLS Verification and `--dangerous`

The `-resolve` option **disables TLS certificate verification**. This is a significant security risk that requires explicit acknowledgment.

### 2.1 Why TLS Verification Must Be Disabled

When connecting by IP instead of hostname:

1. The server's TLS certificate is issued for `cdn.example.com`
2. We're connecting to `192.168.1.100`
3. Certificate validation fails: hostname mismatch
4. Only way to proceed: disable verification with `-tls_verify 0`

```
Normal flow:
  URL: https://cdn.example.com/...
  DNS: cdn.example.com → 203.0.113.50
  TLS: Certificate for cdn.example.com ✓

With -resolve 192.168.1.100:
  URL: https://192.168.1.100/...  (rewritten)
  DNS: BYPASSED
  TLS: Certificate for cdn.example.com, connecting to 192.168.1.100 ✗
       → Must disable verification
```

### 2.2 Security Implications

| Risk | Description |
|------|-------------|
| **MITM Attacks** | Without cert verification, an attacker could intercept traffic |
| **Wrong Server** | No cryptographic proof you're talking to the intended server |
| **Data Exposure** | Encrypted but not authenticated — attacker can read/modify |

### 2.3 The `--dangerous` Safety Gate

To prevent accidental misuse, `-resolve` **requires** the `--dangerous` flag:

```bash
# This will ERROR with a helpful message:
go-ffmpeg-hls-swarm -resolve 192.168.1.100 https://cdn.example.com/live/master.m3u8

# Error output:
# Error: --resolve requires --dangerous flag (TLS verification will be disabled)
#
# ⚠️  WARNING: Using -resolve disables TLS certificate verification.
# This means:
#   - Man-in-the-middle attacks are possible
#   - You have no cryptographic proof of server identity
#   - Traffic can be intercepted and modified
#
# Only use this for:
#   - Load testing in controlled environments
#   - Testing specific servers you control
#   - Debugging with known-good infrastructure
#
# If you understand these risks, add --dangerous to proceed.

# This will work (user explicitly acknowledged the risk):
go-ffmpeg-hls-swarm -resolve 192.168.1.100 --dangerous https://cdn.example.com/live/master.m3u8
```

### 2.4 Runtime Warnings

When `--dangerous` is used, the tool emits persistent warnings:

```json
{"level":"warn","ts":"...","msg":"⚠️  DANGEROUS MODE ENABLED: TLS verification disabled"}
{"level":"warn","ts":"...","msg":"⚠️  Connecting to IP 192.168.1.100 instead of DNS resolution"}
{"level":"warn","ts":"...","msg":"⚠️  Traffic is NOT cryptographically authenticated"}
```

These warnings are:
- Emitted at startup
- Repeated every 60 seconds while running
- Included in any error reports
- Visible in the exit summary

### 2.5 When It's Acceptable

Using `--dangerous` is reasonable when:

| Scenario | Why It's OK |
|----------|-------------|
| Load testing in isolated lab | No real users, controlled environment |
| Testing servers you operate | You control both ends |
| Debugging CDN edge selection | Temporary diagnostic use |
| Testing before DNS propagation | New deployment verification |
| Internal network testing | Traffic doesn't leave trusted network |

### 2.6 When NOT to Use

| Scenario | Why It's Dangerous |
|----------|-------------------|
| Production traffic | Real user data at risk |
| Over public internet | Actual MITM risk |
| With authentication tokens | Credentials could leak |
| Automated/unattended | No human to verify safety |
| Testing third-party services | You don't control the server |

---

## 3. SNI/TLS Limitations

When using `-resolve`, there are additional edge cases:

| Scenario | Behavior | Notes |
|----------|----------|-------|
| Server requires SNI match | May fail even with `-tls_verify 0` | Some servers reject if SNI doesn't match expected hostname |
| CDN requires specific Host | Works (we set Host header) | Most CDNs route on Host header |
| Server only accepts IP literal | Works | No hostname validation |
| Mutual TLS (mTLS) | Not supported | Would need client cert options |

**FFmpeg SNI behavior**: By default, FFmpeg sends SNI matching the URL hostname. With IP override:
- URL: `https://192.168.1.100/...`
- SNI sent: `192.168.1.100` (not the original hostname)

Some CDNs may reject connections where SNI doesn't match their expected hostnames. If you encounter this:

1. The server may be strictly validating SNI (unusual but possible)
2. Try HTTP instead of HTTPS if security isn't critical for your test
3. Use the `--dangerous` mode only in controlled environments where you understand the server's behavior

**Documented limitation**: We don't currently support overriding SNI separately from the URL hostname. If FFmpeg adds a `-tls_servername` option in the future, we could expose it.

---

## 4. Implementation

### Validation

```go
// config/validate.go

func (c *Config) Validate() error {
    if c.FFmpeg.ResolveIP != "" && !c.FFmpeg.DangerousMode {
        return &DangerousModeRequiredError{
            Feature: "resolve",
            Reason:  "TLS verification will be disabled",
        }
    }
    return nil
}

type DangerousModeRequiredError struct {
    Feature string
    Reason  string
}

func (e *DangerousModeRequiredError) Error() string {
    return fmt.Sprintf("--%s requires --dangerous flag (%s)", e.Feature, e.Reason)
}
```

### Warning Emission

```go
// internal/dangerous/warnings.go

// Called when --dangerous is used
func EmitWarnings(logger *slog.Logger, config *Config) {
    logger.Warn("⚠️  DANGEROUS MODE ENABLED: TLS verification disabled")
    if config.FFmpeg.ResolveIP != "" {
        logger.Warn("⚠️  Connecting to IP instead of DNS resolution",
            "ip", config.FFmpeg.ResolveIP)
    }
    logger.Warn("⚠️  Traffic is NOT cryptographically authenticated")
}

// Start a goroutine to repeat warnings periodically
func StartPeriodicWarnings(ctx context.Context, logger *slog.Logger, config *Config) {
    ticker := time.NewTicker(60 * time.Second)
    go func() {
        for {
            select {
            case <-ctx.Done():
                ticker.Stop()
                return
            case <-ticker.C:
                logger.Warn("⚠️  REMINDER: Running in dangerous mode (TLS verification disabled)")
            }
        }
    }()
}
```

### Error Message

```go
// cmd/go-ffmpeg-hls-swarm/main.go

func printDangerousHelp() {
    fmt.Fprintln(os.Stderr, `
⚠️  WARNING: Using -resolve disables TLS certificate verification.
This means:
  - Man-in-the-middle attacks are possible
  - You have no cryptographic proof of server identity
  - Traffic can be intercepted and modified

Only use this for:
  - Load testing in controlled environments
  - Testing specific servers you control
  - Debugging with known-good infrastructure

If you understand these risks, add --dangerous to proceed.
`)
}
```
