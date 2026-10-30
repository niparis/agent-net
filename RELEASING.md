# Releasing

## Make a release

1. Make sure `main` is green and has what you want to ship.
2. Tag a version and push the tag:

   ```
   git tag v0.2.0
   git push origin v0.2.0
   ```

Use a tag that starts with `v`. The release workflow (`.github/workflows/release.yml`) runs on every `v*` tag. It:

1. runs `go vet ./...` and `go test -race ./...` only when the tagged commit has no green CI run,
2. builds `agent-net-{linux,darwin}-{amd64,arm64}` with `CGO_ENABLED=0 go build -trimpath` and sets the version from the tag (`-X main.version=<tag>`),
3. writes `checksums.txt` (SHA-256 of the four binaries),
4. creates a GitHub release with generated notes and attaches the four binaries, `checksums.txt` and `install.sh`.

The release reuses a green CI run on the same commit: when the `ci` workflow already concluded `success` for the tagged commit, the release skips `go vet` and `go test -race` and goes straight to the build. If no green run is found, it runs them itself. Tag only commits that CI has passed.

`install.sh` downloads the latest release and checks the binary against `checksums.txt`, so a release without that file cannot be installed.

## The install line

`https://niparis.github.io/agent-net/install` serves `install.sh` from `main`, not from a release. The pages workflow (`.github/workflows/pages.yml`) publishes it when a push to `main` changes `install.sh` or the workflow itself. It checks the syntax with `sh -n` first. To republish by hand, run the workflow from the Actions tab.

Because the script is served from `main` and downloads the latest release, a change to `install.sh` goes live when it is merged, before any release.

## Update your machines

1. On the gateway machine, run the install line again:

   ```
   curl -fsSL https://niparis.github.io/agent-net/install | sh
   ```

   It installs the latest release, refreshes the binaries the gateway hands out, and restarts the services.
2. On every other machine, run `agent-net invite` on the gateway machine and paste the new line. A machine that already joined gets the new binary and the refreshed integrations, and skips the join.

To test an unreleased build from source, run `scripts/dist.sh` on the gateway machine (it builds the four binaries into the dist directory and installs the one for this machine), then `agent-net setup --gateway`.
