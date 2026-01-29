# Runnable apps
{ pkgs, lib, meta, package, welcome-app }:
let
  mkApp = drv: {
    type = "app";
    program = lib.getExe drv;
  };
in
{
  default = mkApp welcome-app;
  welcome = mkApp welcome-app;

  build = mkApp (pkgs.writeShellApplication {
    name = "${meta.pname}-build";
    text = ''
      echo "Building ${meta.pname}..."
      nix build --print-out-paths
    '';
  });

  run = mkApp (pkgs.writeShellApplication {
    name = "${meta.pname}-run";
    runtimeInputs = meta.runtimeDeps;
    text = ''exec ${lib.getExe package} "$@"'';
  });

  # Unified CLI entry point (dispatcher pattern)
  up = mkApp (pkgs.writeShellApplication {
    name = "swarm-up";
    runtimeInputs = [ pkgs.bash pkgs.jq ];
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

        # Get profiles from single source of truth (contract-first)
        PROFILES=$(nix eval --impure --expr '
          let
            flake = builtins.getFlake (toString ./.);
            pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
            lib = pkgs.lib;
            profileConfig = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
          in
          lib.concatStringsSep " " profileConfig.profiles
        ' --raw 2>/dev/null || echo "default low-latency 4k-abr stress-test logged debug")

        # Try gum first, fallback to bash select
        if command -v gum >/dev/null 2>&1; then
          PROFILE=$(echo "$PROFILES" | tr ' ' '\n' | gum choose --header "Select Profile:")
          TYPE=$(echo -e "runner\ncontainer\nvm" | gum choose --header "Select Deployment Type:")
        else
          # Fallback to bash select (no external dependency)
          echo "Select Profile:"
          select profile in $PROFILES; do
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

      # Resolve underlying package/app (contract-first: use single source)
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

  # Auto-generate shell completion from single source of truth
  generate-completion = mkApp (pkgs.writeShellApplication {
    name = "generate-completion";
    runtimeInputs = [ pkgs.bash pkgs.nix pkgs.jq ];
    text = ''
      set -euo pipefail

      # Extract profiles from single source of truth (via flake, not <nixpkgs>)
      PROFILES=$(nix eval --impure --expr '
        let
          flake = builtins.getFlake (toString ./.);
          pkgs = flake.inputs.nixpkgs.legacyPackages.x86_64-linux;
          lib = pkgs.lib;
          profileConfig = import ./nix/test-origin/config/profile-list.nix { inherit lib; };
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
complete -F _swarm_up 'nix run .#up'
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
}
