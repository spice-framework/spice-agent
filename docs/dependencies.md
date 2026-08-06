# Dependency Review

The core SDK is standard-library-first. Its sole direct dependency is
`github.com/spice-framework/spice v0.1.0-preview.1` (Apache-2.0), used for
portable starter identity and, in the composition phase, annotation descriptors.
It performs no network I/O and introduces no runtime container. Provider,
transport, Protobuf, OpenAI, and Bubble Tea dependencies belong to their owning
repositories or later boundary packages and require separate reviews.
