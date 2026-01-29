# CI checks - Tiered structure
# Default: quick checks (fast, local-friendly)
# Explicit tiers: quick, build, full
{ pkgs, lib, meta, src, package }:

let
  # Quick checks: fast validation (fmt/vet/lint/unit tests + cheap Nix eval)
  quick = {
    format = meta.mkGoCheck {
      inherit src;
      name = "format";
      script = ''
        unformatted=$(gofmt -l .)
        [ -z "$unformatted" ] || { echo "Unformatted:"; echo "$unformatted"; exit 1; }
      '';
    };

    vet = meta.mkGoCheck {
      inherit src;
      name = "vet";
      script = "go vet ./...";
    };

    lint = meta.mkGoCheck {
      inherit src;
      name = "lint";
      script = "golangci-lint run ./...";
    };

    test = meta.mkGoCheck {
      inherit src;
      name = "test";
      script = "go test -v ./...";
    };

    # Cheap Nix evaluation checks (no builds)
    nix-eval = pkgs.writeShellApplication {
      name = "nix-eval";
      runtimeInputs = [ pkgs.nix pkgs.jq ];
      text = ''
        exec ${../scripts/nix-tests/test-eval.sh}
      '';
    };

    shellcheck = pkgs.writeShellApplication {
      name = "shellcheck";
      runtimeInputs = [ pkgs.shellcheck ];
      text = "exec ${../scripts/nix-tests/shellcheck.sh}";
    };
  };

  # Build checks: build key packages/containers (default profile only)
  build = quick // {
    build-core = package;  # Core Go binary
  };

  # Full checks: build all profiles/variants (CI-only / opt-in)
  full = build // {
    # All profile builds (via test scripts)
    nix-tests = pkgs.writeShellApplication {
      name = "nix-tests";
      runtimeInputs = [ pkgs.bash pkgs.nix ];
      text = ''
        exec ${../scripts/nix-tests/test-all.sh}
      '';
    };
  };
in
{
  # Default: quick checks (fast, ~30 seconds)
  default = quick;

  # Explicit tiers
  quick = quick;
  build = build;
  full = full;
}
