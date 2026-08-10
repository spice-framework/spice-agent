# Provisional Git workflow benchmark

Run `make benchmark` for five deterministic single-CPU 500-iteration samples
of the authority-token binding path. It records time and allocations without
executing Git. Process startup and repository size are host-dependent and are
therefore measured by real conformance tests rather than made a preview
compatibility budget.

Promotion must add a material-regression threshold after Windows and Linux
baselines are recorded against the released verified-child launcher.

Initial Windows amd64 baseline on Go 1.26.5 (Ryzen 9 5900X): 3.00–4.34
microseconds, 2,128–2,129 bytes, and 58 allocations per token over five
500-iteration samples. These values are evidence, not a compatibility promise.
