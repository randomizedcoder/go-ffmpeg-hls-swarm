# Contributing to go-ffmpeg-hls-swarm

First off, thanks for considering contributing! 🎉

> 📐 **Current Status: Design Complete, Implementation Starting**
>
> The architecture is finalized. Right now, the most valuable contributions are **implementation**, **design feedback**, and **documentation improvements**.

## Current State

| Phase | Status | What's There |
|-------|--------|--------------|
| Design | ✅ Complete | Architecture, interfaces, FFmpeg research |
| Implementation | 🚧 Starting | You could be the first contributor! |
| Testing | ⏳ Waiting | Needs implementation first |

## Ways to Contribute Right Now

### 🔨 Implementation (High Impact!)

Ready to write code? The design docs are your specification:

1. Read [DESIGN.md](docs/DESIGN.md) for architecture overview
2. Check the implementation roadmap (Phase 1: MVP)
3. Pick a component and open a PR!

**Good starting points:**
- `internal/config` — CLI parsing and validation
- `internal/preflight` — Startup checks (ulimit, ffmpeg detection)
- `internal/supervisor` — Backoff calculation logic

### 🔬 Design Feedback

Found something unclear or have a better approach?

- Read the design docs, especially [DESIGN.md](docs/DESIGN.md), [SUPERVISION.md](docs/SUPERVISION.md)
- Open an issue with the `design` label
- Suggest alternatives with rationale

### 📝 Documentation

- Fix typos, improve clarity
- Add examples that would have helped you
- Identify gaps in explanations

### 🧪 Research

- Test FFmpeg HLS behavior edge cases
- Document how different CDNs/origins behave
- Validate assumptions in design docs

---

## Once Implementation Begins

When code exists, you'll be able to:

### Prerequisites

- Go 1.21 or later
- FFmpeg (for integration testing)
- Git

### Setup

```bash
# Clone the repo
git clone https://github.com/randomizedcoder/go-ffmpeg-hls-swarm.git
cd go-ffmpeg-hls-swarm

# Install dependencies
go mod download

# Run tests
go test ./...

# Build
go build -o go-ffmpeg-hls-swarm ./cmd/go-ffmpeg-hls-swarm
```

---

## Ways to Contribute (Code)

### 🐛 Found a Bug?

Open an issue! Include:
- What you were trying to do
- What happened instead
- Steps to reproduce (if possible)
- Your environment (OS, Go version, FFmpeg version)

### 💡 Have an Idea?

Open an issue to discuss it first. This helps avoid duplicate work and lets us think through the design together.

Some areas we're particularly interested in:
- Alternative process runners (not just FFmpeg)
- Better metrics and observability
- Performance optimizations
- Testing strategies for load testing tools (meta, right?)

### 🔧 Want to Code?

1. **Check existing issues** — look for `good first issue` or `help wanted` labels
2. **Comment on the issue** — let us know you're working on it
3. **Fork and branch** — `git checkout -b your-feature-name`
4. **Write tests** — especially for failure scenarios
5. **Submit a PR** — reference the issue number

## Code Style

We try to keep things simple and readable:

- **Go conventions** — `gofmt`, `golint`, etc.
- **Clear names** — prefer `clientManager` over `cm`
- **Comments for "why"** — code shows what, comments explain why
- **Table-driven tests** — especially for edge cases and failures
- **Error handling** — wrap errors with context, don't swallow them

### Example Test Style

```go
func TestBackoff_Calculate(t *testing.T) {
    tests := []struct {
        name    string
        attempt int
        wantMin time.Duration
        wantMax time.Duration
    }{
        {
            name:    "first attempt",
            attempt: 0,
            wantMin: 200 * time.Millisecond,
            wantMax: 300 * time.Millisecond,
        },
        // More cases...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // Test implementation
        })
    }
}
```

## Pull Request Process

1. **Keep PRs focused** — one feature or fix per PR
2. **Update docs** — if you change behavior, update relevant docs
3. **Add tests** — especially for new code paths
4. **Write a good description** — explain what and why

### PR Template

```markdown
## What

Brief description of the change.

## Why

Why is this change needed? Link to issue if applicable.

## How

How does this implementation work? Any trade-offs?

## Testing

How did you test this? Any manual testing steps?
```

## Design Decisions

Big changes should start with a discussion. We value:

- **Simplicity** — prefer boring, obvious solutions
- **Flexibility** — interfaces over concrete types where it makes sense
- **Observability** — if it can fail, we should be able to see it failing
- **Graceful degradation** — partial failures shouldn't bring down everything

See [docs/DESIGN.md](docs/DESIGN.md) for the overall architecture and philosophy.

## Community

- Be kind and respectful
- Assume good intent
- Ask questions — there are no dumb questions
- Help others when you can

## Questions?

Open an issue with the `question` label, or just ask in any relevant issue/PR. We're friendly!

---

Thanks again for your interest in contributing. Looking forward to building something useful together! 🚀
