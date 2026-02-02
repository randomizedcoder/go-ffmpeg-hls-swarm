# Contributing Guide

Thank you for your interest in contributing to go-ffmpeg-hls-swarm!

---

## Getting Started

### Prerequisites

- Go 1.25+
- FFmpeg with HLS support
- Nix (optional, recommended)

### Development Setup

```bash
# Clone
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Enter development shell (provides all tools)
nix develop
# Or: make dev

# Build
make build

# Run tests
make test
```

---

## Development Workflow

### Building

```bash
make build          # Build binary to ./bin/
make build-nix      # Build with Nix (reproducible)
```

### Testing

```bash
make test           # Run Go unit tests
make test-race      # Run with race detector
make lint           # Run linters
make check          # All checks
```

### Formatting

```bash
make fmt            # Format Go code
make fmt-nix        # Format Nix files
make fmt-all        # Format everything
```

---

## Code Structure

```
go-ffmpeg-hls-swarm/
├── cmd/go-ffmpeg-hls-swarm/   # CLI entry point
├── internal/
│   ├── config/                 # Configuration and flags
│   ├── orchestrator/           # Client lifecycle management
│   ├── supervisor/             # Process supervision
│   ├── process/                # FFmpeg process building
│   ├── metrics/                # Prometheus metrics
│   ├── parser/                 # FFmpeg output parsing
│   ├── preflight/              # System checks
│   ├── stats/                  # Statistics aggregation
│   ├── tui/                    # Terminal UI
│   └── logging/                # Structured logging
├── nix/                        # Nix configurations
├── scripts/                    # Load test scripts
├── docs/                       # Legacy documentation
└── documents/                  # New documentation
```

---

## Guidelines

### Code Style

- Follow standard Go conventions
- Use `gofumpt` for formatting
- Run `golangci-lint` before committing
- Add tests for new functionality

### Commit Messages

Format:

```
<type>: <subject>

<body>

Co-Authored-By: Claude Opus 4.5 <noreply@anthropic.com>
```

Types:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation
- `refactor`: Code refactoring
- `test`: Adding tests
- `chore`: Maintenance

### Pull Requests

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests: `make check`
5. Submit a pull request

---

## Ways to Contribute

### Code

- Bug fixes
- New features
- Performance improvements
- Test coverage

### Documentation

- Fix typos and errors
- Improve explanations
- Add examples
- Update outdated content

### Testing

- Report bugs
- Test on different platforms
- Improve test coverage

### Design

- Propose features
- Review architecture decisions
- Provide feedback

---

## Running the Full Test Suite

```bash
# All checks
make ci

# Nix tests
make test-nix-all

# Integration tests (Linux only)
make test-integration
```

---

## Questions?

- Open an issue for bugs or feature requests
- Check existing documentation
- Review existing issues before creating new ones

---

## License

By contributing, you agree that your contributions will be licensed under the MIT License.
