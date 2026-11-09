# Architecture

Bales is one HTTP process and a set of stateless replicas. The gateway keeps
only soft state: a cache belief per replica, an in-flight leader registry, and a
virtual-deadline queue. Losing the gateway loses scheduling state, not model
state.

## Request path

1. `POST /v1/chat/completions` reads the JSON body and concatenates message text.
2. The tenant string and a deployment salt seed a chained block hash over the
   prompt. The final hash identifies the full prompt chain.
3. Prompt tokens are estimated as `bytes * tokens_per_byte`. The estimate is
   used for admission, routing and the hold rule. It is not used for billing.
4. Admission caps queued estimated work at `QMaxMS`. Overload returns 429.
5. The request receives a static virtual deadline:
   `d_i = arrival + D_class + eta * estimated_cost`.
6. A single dispatcher pops the earliest deadline and chooses one of three
   actions: dispatch, hold, or spill.
7. The proxy streams the engine response to the client, records TTFT, total
   time, completion tokens and engine-reported cached tokens.
8. A telemetry row is written. The directory auditor compares predicted cached
   tokens with `usage.prompt_tokens_details.cached_tokens`.

## Coalescing

The registry maps a chain hash to one in-flight leader:

```text
Group{key, replica, started, est_done, done, outcome, followers}
```

When a follower arrives with the same full chain:

- `tau` is the leader's remaining estimated prefill.
- `saving` is `cost(q+L, 0) - cost(q, L)` from the calibrated model. For an
  exact prompt duplicate `q` is zero and the saving is the value of the whole
  cached prefix.
- Hold when `saving - tau - (wait_leader - wait_best) > epsilon`, the leader
  finishes inside the follower's slack, and `tau` is below the hold cap.
- The hold deadline is `min(1.5 * tau, slack, delta_max)`.
- A leader closes its group on the first response byte. A leader that exits
  before the first byte closes the group as failed, and followers spill.
- The registry is per tenant salt, so followers cannot observe another tenant's
  in-flight prompts.

The prototype coalesces exact prompt chains. A trie over nested in-flight chains
is the next step for agent fan-out workloads with different suffixes.

## Queue

Deadlines are static, so queue order is a function of arrival time and class,
not of the current clock. Insert is `O(log n)` and the heap never needs
reheapification.

The wait bound for a tagged request is:

```text
W_i <= (Q_max + A(delta_i)) / C + s_max
```

`A` is the arrival curve of outranking requests inside the overtaking window
`delta_i = offset_i - min_offset`, `C` is pool work capacity per millisecond, and
`s_max` is the largest non-preemptive service. Admission keeps the first term
finite. The unit test fuzzes 200 random traces against this bound.

## Routing

`bales_ect` scores each healthy replica in milliseconds:

```text
ECT = queue_wait + cost(uncached, cached) / speed + interference
```

Placements are rejected when KV usage would exceed capacity or when the
predicted decode-step inflation exceeds the ITL budget. If every replica is
rejected, the policy falls back to the least loaded healthy replica and records
that fact in telemetry.

The affinity gate is a separate function. It compares recoverable work
`rate * saving(prefix)` with a fraction of cluster capacity:

- spread when recoverable work is small,
- pin when one replica can carry the group,
- replicate when the group needs `k` replicas.

## Directory belief and audit

The directory is a belief, not a source of truth. The gateway updates it at
routing time to mirror llm-d speculative indexing. Engine-reported
`cached_tokens` tells the auditor whether the belief was right. Precision and
recall are exported at `/metrics`. A future precise mode would consume engine KV
events, but the prototype does not.

## Telemetry contract

`internal/telemetry` writes 24 columns: timestamp, request and group identity,
class, model, prompt and cached token counts, replica, policy and reason,
coalescing fields, hold time and outcome, queue and prefill estimates, TTFT,
total time, completion tokens, status, stream flag, slack, and error. Every
number in `README.md` has a row in `evidence/claims.csv` that points either to a
primary source or to one of these artifacts.

## Failure handling

- A dead leader is closed as failed; waiting followers spill on their deadline.
- A follower whose deadline fires first routes normally and records `deadline`.
- A client disconnect cancels the proxy and releases the reserved load.
- A replica that fails three metric scrapes is marked unhealthy. Routing skips
  it until a scrape succeeds.
- The gateway is stateless enough to restart. In-flight clients see a closed
  connection; no cache state is lost.

## Simulator

`internal/sim` is a discrete-event model with one prefill slot per replica and
no cross-request decode batching. It tracks two directories: the speculative
belief used for routing and the actual cache used for prefill cost. That gap is
the duplicate-prefill mechanism the coalescer addresses. The simulator is for
relative comparisons. Fidelity against real T4 runs is an explicit gate in the
plan and has not been measured.
