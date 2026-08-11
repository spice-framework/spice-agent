# Phase 3 live Responses-compatible provider acceptance

Provider commit `f1e2b7a78bd199b96598a441acb29be5461b8bb7` completed the
canonical live-provider criterion with exactly one inference request through
OpenRouter's OpenAI Responses-compatible endpoint and exact model
`poolside/laguna-s-2.1:free`.

The proof fails closed unless the caller explicitly supplies the opt-in, API
key, base URL, and model. It makes a bounded public-catalog preflight and
requires the exact `:free` route to advertise zero prompt and completion
prices. The model operation has no retries, tools, or transcript persistence,
and the entire acceptance path has a 90-second bound. Ordinary verification
does not opt in and has no default network fallback.

The committed evidence is deliberately limited to:

- endpoint host class `openrouter`;
- exact model `poolside/laguna-s-2.1:free`;
- inference request count `1`;
- maximum elapsed time `90000` milliseconds;
- result SHA-256
  `969f379b637f02b0d849fef40b3de43c18fe1d966c15bddc3f91e93bfe94fdc6`;
  and
- catalog cost-zero result `true`.

No API key, prompt, output, transcript, token data, or complete endpoint URL is
persisted. Provider hosted CI run
[`31462356973`](https://github.com/spice-framework/spice-agent-provider-openai/actions/runs/31462356973)
and documentation run
[`31462356963`](https://github.com/spice-framework/spice-agent-provider-openai/actions/runs/31462356963)
passed for that commit.

This proves the provider adapter against a real live OpenAI
Responses-compatible service. It is not first-party OpenAI service evidence.
The separate mutually exclusive first-party mode remains optional and requires
its own explicit OpenAI endpoint, model, credential, and opt-in.
