# go-ffmpeg-hls-swarm Documentation

Welcome to the documentation for go-ffmpeg-hls-swarm, a specialized HLS load testing tool that orchestrates FFmpeg processes to simulate concurrent viewers.

## Documentation Structure

### User Guide

For users who want to run load tests:

| Document | Description |
|----------|-------------|
| [Getting Started](user-guide/getting-started/) | Installation and first steps |
| [Configuration](user-guide/configuration/) | CLI flags, profiles, and environment variables |
| [Operations](user-guide/operations/) | Running tests, OS tuning, troubleshooting |
| [Observability](user-guide/observability/) | Metrics, TUI dashboard, monitoring |

### Contributor Guide

For developers who want to understand or modify the codebase:

| Document | Description |
|----------|-------------|
| [Architecture](contributor-guide/architecture/) | High-level design and package structure |
| [Components](contributor-guide/components/) | Deep-dives into internal packages |
| [Infrastructure](contributor-guide/infrastructure/) | Nix, containers, MicroVMs, CI/CD |
| [Security](contributor-guide/security/) | Security model and threat considerations |
| [Testing](contributor-guide/testing/) | Test strategy and test origin server |

### Reference

Quick-lookup documentation:

| Document | Description |
|----------|-------------|
| [CLI_FLAGS.md](reference/CLI_FLAGS.md) | Complete CLI flag reference |
| [METRICS_REFERENCE.md](reference/METRICS_REFERENCE.md) | All Prometheus metrics |
| [PORTS.md](reference/PORTS.md) | Port assignments |
| [EXIT_CODES.md](reference/EXIT_CODES.md) | Exit code reference |
| [FFMPEG_HLS_REFERENCE.md](reference/FFMPEG_HLS_REFERENCE.md) | FFmpeg HLS internals |

### Archive

Historical documentation (design docs, implementation logs):

| Folder | Description |
|--------|-------------|
| [archive/design-docs/](archive/design-docs/) | Pre-implementation design documents |
| [archive/implementation-logs/](archive/implementation-logs/) | Work-in-progress tracking |
| [archive/implementation-plans/](archive/implementation-plans/) | Step-by-step implementation plans |
| [archive/analysis/](archive/analysis/) | Technical investigations |
| [archive/defects/](archive/defects/) | Bug tracking documents |

---

## Quick Links

- **New to the project?** Start with [QUICKSTART.md](user-guide/getting-started/QUICKSTART.md)
- **Running your first test?** See [FIRST_LOAD_TEST.md](user-guide/getting-started/FIRST_LOAD_TEST.md)
- **Need CLI help?** Check [CLI_REFERENCE.md](user-guide/configuration/CLI_REFERENCE.md)
- **Want to contribute?** Read [CONTRIBUTING.md](contributor-guide/CONTRIBUTING.md)
