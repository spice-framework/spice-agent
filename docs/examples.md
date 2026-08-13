# Examples

The canonical compiled-extension examples are separate released modules, not
copies embedded in this repository:

- [`spice-agent-tool-text`](https://github.com/spice-framework/spice-agent-tool-text)
  demonstrates a deterministic text tool;
- [`spice-agent-tool-json`](https://github.com/spice-framework/spice-agent-tool-json)
  demonstrates bounded structured input; and
- [`spice-agent-tool-integer`](https://github.com/spice-framework/spice-agent-tool-integer)
  demonstrates typed numeric behavior.

Each was authored from public documentation with released modules and a fresh
cache, contains no `replace` directive or private import, and proves install,
configure, debug, test, package, and delete. Pin the exact versions and sums in
[`compatibility/public-authoring.json`](../compatibility/public-authoring.json)
when reproducing the exercises.

For client authors, [`client/conformance`](../client/conformance) is executable
example code for the transport-neutral lifecycle. For runtime-plugin authors,
[`plugin/conformance`](../plugin/conformance) is the corresponding process
protocol kit. Product distributions, providers, terminal UIs, and coding tools
remain independently versioned repositories; their code is not a hidden Agent
example contract.
