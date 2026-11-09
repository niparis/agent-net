# Releasing

## Make a release

CI is the release gate. Do not run the long local race suite to release; use `scripts/test.sh` while developing.

1. Push the commit you want to ship to a branch, so CI runs on it:

   ```
   git push origin HEAD:refs/heads/release-v0.2.0
   ```

2. Wait until the `ci` workflow passes on that exact commit (formatting, `go vet`, all Go tests including the daemon and gateway shards, the Pi and OpenCode plugin tests, and the installer syntax).
3. Move `main` to that commit. It must be a fast-forward:

   ```
   git push origin <sha>:refs/heads/main
   ```

4. Tag that commit and push the tag, as two separate commands:

   ```
   git tag -a v0.2.0 -m v0.2.0 <sha>
   git push origin v0.2.0
   ```

5. Delete the release branch.

Use a tag that starts with `v`. The release workflow (`.github/workflows/release.yml`) runs on every `v*` tag. It:

1. runs `go vet ./...` and `scripts/test.sh -- -timeout 30m` only when the tagged commit has no green CI run,
2. builds `agent-net-{linux,darwin}-{amd64,arm64}` with `CGO_ENABLED=0 go build -trimpath` and sets the version from the tag (`-X main.version=<tag>`),
3. writes `checksums.txt` (SHA-256 of the four binaries and `install.sh`), signs its exact bytes with the `RELEASE_SIGNING_KEY` Actions secret, and independently verifies both signatures against the tagged public keys,
4. creates a GitHub release with generated notes and attaches the binaries, checksum file, both signatures and `install.sh`,
5. dispatches the Pages workflow to publish the latest release installer.

The release reuses a green CI run on the same commit: when the `ci` workflow already concluded `success` for the tagged commit, the release skips `go vet` and the test suite and goes straight to the build. If no green run is found, it runs them itself. Tag only commits that CI has passed. Protect the `v*` tag namespace with a GitHub ruleset that limits who can create, update or delete release tags.

The workflow refuses to publish if the secret is missing, signing fails, or the secret does not match both public keys in the tagged commit. Go verifies the raw Ed25519 signature in `checksums.txt.sig`. The installer verifies the OpenSSH SSHSIG in `checksums.txt.sshsig`; both sign the same exact checksum file with the same key.

## The install line

`https://niparis.github.io/agent-net/install` serves `install.sh` from the latest published GitHub release. The Pages workflow runs on a published release or by manual dispatch. A release created with the Actions `GITHUB_TOKEN` does not trigger release workflows, so the release workflow also dispatches Pages explicitly. Pages downloads the latest release's installer and signed checksum files, verifies both signatures and the installer checksum against the public key in the checked-out source, and checks the installer syntax before publishing. Merging an installer change alone does not publish it.

## Release signing key

The release public key is in `internal/upgrade/release.go` and `install.sh`. The matching private key is in the repository Actions secret `RELEASE_SIGNING_KEY`. The key was put in place on 2026-11-04. Do not generate a new key for an ordinary release.

For the first signed release, follow the release steps above. After Pages publishes the new installer, run the install line on the gateway, then upgrade the other machines. Wait until the Pages workflow has deployed: release creation and Pages deployment are separate steps. The old `agent-net upgrade` cannot copy the new manifest and signature to the gateway dist directory; if it was used, run the install line on the gateway afterward to put them there. Older binaries and the previously published installer do not check signatures while moving to this first release. Once the new code is installed, later unsigned or incorrectly signed releases are refused. A signed client also refuses upgrades from an older gateway that lacks the signed manifest. The gateway still chooses which signed release it serves to joined machines. Manual and pushed downgrades require `--force`; automatic upgrades only move forward. A gateway invite install trusts the pinned gateway to provide the binary, not the release signature.

### Rotate the key

Only for a planned rotation or after compromise:

1. Generate a key on a trusted machine, outside the repository, at a new private path:

   ```
   go run ./scripts/release-key generate /path/to/private-release-key.pem
   ```

   It writes an unencrypted PKCS#8 Ed25519 key with mode 0600, refuses to overwrite a file, and prints only its two public encodings. Store the private key securely and keep an offline backup. Never commit it or paste it into a log.
2. Put the printed Go value in `ReleasePublicKeyBase64` in `internal/upgrade/release.go`. Put the printed SSH value in `RELEASE_SSH_PUBLIC_KEY` in `install.sh`. Commit both changes together.
3. Replace the repository Actions secret `RELEASE_SIGNING_KEY` with the new PEM, then publish the release. The workflow checks that the secret matches both public keys and verifies both signatures before creating the release.
4. After Pages deploys, bootstrap the gateway and every other machine with the new installer over authenticated HTTPS. Old binaries cannot verify a release signed only by the new key; their manual and automatic upgrades cannot cross the rotation. This scheme has no automatic signed key rollover.

If the private key is compromised, consider it burned and assume signatures under the old key can be forged. Removing it from the Actions secret does not revoke the old public key embedded in installed binaries. Turn off auto-upgrade on every machine that cannot yet be bootstrapped, using `agent-net setup --no-auto-upgrade` where available, or stop its daemon or disconnect it until the new installer is ready. Auto-upgrade off alone does not block a manually requested or pushed upgrade. Do not accept old-key releases. Replace the secret, publish the new-key release from a trusted commit, and bootstrap every machine with the new installer as above.

### Roll back a gateway

`agent-net upgrade --rollback` clears the gateway's signed checksum manifest before it runs setup from the previous binary. A later `setup --gateway` that changes a dist binary also removes the manifest and signature. Signed nodes refuse upgrades from that gateway until a complete matching signed release is installed. To restore one, run the published installer on the gateway with `--gateway --version vX.Y.Z` for a signed release using the current key. The old gateway's generated checksums remain available to older clients that do not require signatures.

OpenSSH 8.1 introduced `ssh-keygen -Y` signing and verification. Current macOS and common Linux installations include OpenSSH, but a minimal Linux install or an older OS may not have a suitable `ssh-keygen`; the installer stops with an error there. See [OpenSSH 8.1 release notes](https://www.openssh.org/txt/release-8.1). The Go upgrade path uses the standard library and does not need OpenSSH.

## Update your machines

1. On the gateway machine, run the install line again:

   ```
   curl -fsSL https://niparis.github.io/agent-net/install | sh
   ```

   It installs the latest release, refreshes the binaries the gateway hands out, and restarts the services.
2. On every other machine, run `agent-net invite` on the gateway machine and paste the new line. A machine that already joined gets the new binary and the refreshed integrations, and skips the join.

To test an unreleased build from source, run `scripts/dist.sh` on the gateway machine (it builds the four binaries into the dist directory and installs the one for this machine), then `agent-net setup --gateway`.
