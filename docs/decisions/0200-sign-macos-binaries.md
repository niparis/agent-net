# 0200 - Sign agent-net binaries on each Mac with a local identity

Date: 2026-11-28
Status: Decided by the owner on 2026-11-28 in the chief of staff's chat ("YES YES YES", owner judgment so_2d13d20bf200940f), on the proposal: a local code-signing certificate per Mac, every agent-net binary signed with it under a fixed identifier at install and at every update, removed on uninstall.

## Context

After each update macOS asked the owner again: '"agent-net" would like to access data from other apps'. `codesign -dv` on his Mac showed `Identifier=a.out`, `flags=0x20002(adhoc,linker-signed)`, `Signature=adhoc`. The TCC log said `Failed to match existing code requirement` for the binary after each update.

Go links darwin binaries with an ad-hoc signature. TCC stores a permission with the code's designated requirement. For an ad-hoc signature that requirement is the hash of the exact build (its cdhash), so each release is a new program. For a binary signed with a certificate, the requirement is the identifier and the certificate, which stay the same across builds.

There is no Apple developer account, and release signing in CI stays as it is.

## Decision

- The package `internal/macsign` makes one identity per Mac on first use: a self-signed RSA certificate with the code signing purpose, made in Go, imported as a PKCS#12 file into a keychain of agent-net's own in `~/.local/state/agent-net/daemon/codesign/`. Its random password is kept beside it (mode 0600). `security import -T /usr/bin/codesign` and `security set-key-partition-list -S apple-tool:,apple:,codesign:` let `codesign` use the key without a dialog. The keychain is added to the user's search list. The identity's SHA-1 is written last, so a half-made keychain is made again.
- Every path that puts a new binary on a Mac signs it first: `install.sh` and the invite script run the new binary as `agent-net macos-sign <staged file>` before they move it in place; `agent-net upgrade` (joined machine and gateway host) and the daemon's upgrade sign the downloaded file before `upgrade.Replace`. The command is `codesign --force --sign <SHA-1> --keychain <keychain> --identifier dev.agent-net.agent-net --timestamp=none` on a copy, which is then renamed over the file, so a process that runs from it is never changed under it.
- A failure never blocks an install or upgrade. The binary keeps its ad-hoc signature, the command prints why, `last-sign.txt` records it, and the daemon logs that line at start.
- Uninstall runs `security delete-keychain` and deletes the folder.
- Nothing changes on Linux.

## Not done

- No trust setting is added for the certificate: that would change the user's trust settings and may ask for a password. If `codesign` refuses the untrusted certificate on some macOS version, the daemon log says so and the owner can decide on a one-time `security add-trusted-cert`.
- `agent-net upgrade --rollback` puts back the previous file as it was. A binary from before this change stays ad-hoc.
- The gateway's dist binaries for other machines are not signed here: each Mac signs its own copy at install.
