# Development shell
{ pkgs, lib, meta }:
let
  welcome = pkgs.writeShellApplication {
    name = "${meta.pname}-welcome";
    runtimeInputs = meta.runtimeDeps;
    text = ''
      echo ""
      echo "🎬 ${meta.pname} dev shell"
      echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
      echo "Go:     $(go version | cut -d' ' -f3)"
      echo "FFmpeg: $(ffmpeg -version 2>/dev/null | head -1 | cut -d' ' -f3)"
      echo ""
      echo "  go build ./cmd/${meta.pname}  │  go test ./..."
      echo "  golangci-lint run             │  nix build"
      echo ""
    '';
  };
in
{
  default = pkgs.mkShell {
    name = "${meta.pname}-dev";
    packages = meta.goToolchain ++ meta.runtimeDeps ++ meta.devUtils;
    shellHook = ''
      export GOPATH="$PWD/.go"
      export PATH="$PWD/.go/bin:$PATH"
      ${lib.getExe welcome}
    '';
  };
  welcome-app = welcome;
}
