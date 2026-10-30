# 0025 - Install quota-axi for the owner

**Date:** 2026-10-30
**Status:** Built.
**Context:** The Quota integration needed [quota-axi](https://github.com/kunchenguid/quota-axi), a Node CLI, to be installed by hand. The owner asked that users never install it by hand. The docs said "agent-net never installs it", which made the Quota tab empty on a machine where nobody had run npm.

Base: [0021](0021-network-integrations.md) (the network answers and the apply path), [0022](0022-component-graph-and-cto.md) (quota readings).

## The decision

When the Quota integration is on and no quota-axi is found, agent-net installs a pinned copy in its own data folder:

```
npm install --prefix ~/.local/share/agent-net/quota-axi --no-fund --no-audit quota-axi@0.1.69
```

The daemon runs `~/.local/share/agent-net/quota-axi/node_modules/.bin/quota-axi`. The version is one Go constant. Updating the pin is part of an agent-net release. npm checks the package integrity from the registry.

A quota-axi the owner installed always wins: the path in `node.json` `quota_axi`, then `PATH`, then `~/.local/bin`, then on macOS the nvm folders, `/opt/homebrew/bin`, `/usr/local/bin` and the global npm prefix. A copy found in a Node folder runs with that folder first on `PATH`, so it uses the matching Node.

Setup installs it when it applies the integrations. The daemon installs it in the background when it applies a changed network answer, with a 3 minute limit. Startup never waits for npm. A failed install is remembered and retried at most once a day. Every attempt writes one log line.

agent-net does not install Node. Without Node 22.19 or newer, or without npm, it installs nothing and prints one line in `agent-net setup` and in the empty Quotas tab: `quota-axi needs Node 22.19 or newer; install Node and agent-net installs quota-axi for you.`

Turning the Quota integration off removes the private folder. `agent-net uninstall` removes it too.

## Consequences

agent-net now downloads a pinned npm package from the npm registry and runs it as the user. That is a new network fetch at setup or daemon start, and a new copy of code on the machine. The package is MIT licensed, needs Node 22.19 or newer, and its `quota-axi` binary reads the provider logins that are already there. agent-net still runs it with `--no-credential-refresh` and never with `--full`. The reading and its risk are the same as before; only who installs the CLI changed.

A machine without Node keeps an empty Quotas tab and gets the one Node line. A machine with Node gets readings without running npm by hand.

## Alternatives not taken

- **Keep asking the owner to install it.** A new machine shows no quotas until someone reads the docs and runs npm.
- **Install it globally with `npm install -g`.** It writes outside agent-net's folders, needs permissions the owner may not have, and is harder to remove cleanly.
- **Download a tarball ourselves.** npm already checks integrity, resolves the platform files and writes the launcher. A private prefix keeps the result in one folder.
- **Install Node too.** That is a much larger change with its own upgrade path. The owner installs Node; agent-net installs quota-axi.
