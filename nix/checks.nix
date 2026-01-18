# CI checks
{ pkgs, lib, meta, src, package }:
{
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

  build = package;
}
