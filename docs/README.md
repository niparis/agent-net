# Documentation

## Use it

- [setup.md](setup.md): install, add a machine, update, remove. The details behind the README quickstart.
- [reference.md](reference.md): ports, files, environment variables, flags, and what setup changes on your machine.
- [security.md](security.md): the trust model, and what agent-net does not protect against.
- [troubleshooting.md](troubleshooting.md): first checks and fixes for common problems.
- [agent-guidance.md](agent-guidance.md): the text agent-net adds to your agents' instruction files.
- [../plugins/pi/README.md](../plugins/pi/README.md): the Pi extension.

## Work on it

- [../AGENTS.md](../AGENTS.md): build, test, layout and conventions.
- [../RELEASING.md](../RELEASING.md): cut a release and update machines.

## Design history

These files record how the project was planned and built. They are kept for context. They are not kept up to date, and parts of them are out of date (names, ports, plans that changed). For how things work now, use the pages above and the code.

- [brief.md](brief.md): the original brief.
- [review.md](review.md): review of the brief and the build order.
- [decisions/](decisions/): decisions taken, one file each. [0002](decisions/0002-gateway-record-and-inventory.md) covers the gateway log, repo info, harness inventory and retention.
- [spike/](spike/): what we measured about each agent before building.
- [step2-design.md](step2-design.md): protocol and message design for the gateway, daemon and Claude and Pi adapters. Still a good description of the wire protocol.
- [step2-run.md](step2-run.md): how to run the pieces by hand, one by one.
- [step2-e2e.md](step2-e2e.md): results of the first end-to-end test.
