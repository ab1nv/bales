# Claims

The machine-readable ledger is `evidence/claims.csv`. It has one row per claim
with a source or run file, the code commit, the reproduction command and a
verification status.

Current prototype claims, all from simulation or local microbenchmarks:

- The gateway coalesces an exact-prompt follower and releases it on the leader's
  first token. Covered by the gateway integration test.
- In a simulated 32-request cold burst, coalescing lowers P50 TTFT from 504.6 ms
  to 142.5 ms and prefill work from 3886.6 ms to 509.4 ms.
- In the long-context simulation at 6 requests per second, ECT records 1209 ms
  P99 TTFT against 3036 ms for least-token routing.
- At 8 requests per second on one simulated server, strict priority lowers
  stream P99 TTFT from 5025 ms to 562 ms while batch P99 wait rises from 4649 ms
  to 5109 ms.
- The decision core costs 15.9 ns per coalescing decision and 33.9 ns per ECT
  pick on the Apple M5 build host.

What the prototype does not claim:

- No GPU measurements. All cluster numbers are simulated.
- No tuned comparison against llm-d, Dynamo, DualMap, Preble or CacheRoute.
  Those baselines are simplified ports.
- No evidence that the quadratic cost term improves routing on its own. The
  observed long-context gain comes mainly from queue-aware ECT.
- No EDF dominance result. EDF with the default class deadlines matches strict
  priority in the queue experiment.

Every number in the README appears in the ledger. A CI step validates the
ledger's schema and status values. Run `make claims` locally.
