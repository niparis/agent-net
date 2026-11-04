# 0057 - Signed release checksums

Date: 2026-11-04

The release workflow signs the exact SHA-256 checksum file with an Ed25519
private key held in the `RELEASE_SIGNING_KEY` Actions secret. A Go helper
writes a raw signature for Go clients and an SSHSIG with namespace `agent-net`
for the shell installer. It checks that the private key matches both public
keys embedded in the tagged sources before the workflow creates a release.

The gateway keeps the original manifest and raw signature alongside its dist
binaries. Joined machines verify the signature before using a checksum for a
manual, pushed or automatic upgrade. The gateway's generated checksum fallback
remains for old clients and unsigned local builds; new clients require the
signature. A locally built dist is deliberately unsigned.

The installer verifies the SSHSIG with `ssh-keygen -Y verify`, then checks the
binary hashes. It fails closed without a suitable OpenSSH. Pages publishes the
installer from the latest release, not from main. HTTPS still authenticates the
initial installer download. A gateway invite uses the pinned gateway's binary
and is a separate trust path.

The binary and installer each contain one public key. Rotation needs a manual
bootstrap with the newly published installer on every machine; an old binary
cannot authenticate a new-key-only release. Before the first signed release,
older binaries still use their old checksum-only behavior.
