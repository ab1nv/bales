# Evidence ledger

This directory holds the prototype's reproducible evidence.

- `claims.csv` lists every numeric or structural claim used in the README and
  reports, with its source or run ID.
- `manifest.json` records the environment, commit, commands and seed count for
  the checked-in runs.
- `bench.txt` is the raw microbenchmark output.
- `runs/` contains simulator summaries and a raw burst request log. The gateway
  demo writes `runs/demo.csv`.

Cluster results come from the trace-driven simulator in `internal/sim`. Replicas
process one prefill at a time and decode is not batched across requests. The
model is useful for relative comparisons, not for absolute latency claims.
Baselines are simplified ports and have not received the tuning budget planned
for the full evaluation.

Reproduce the checked-in runs with:

```sh
make build
./bin/bales-simulate -exp all -out evidence/runs -seeds 3
go test -run '^$' -bench . -benchmem -benchtime=1s ./internal/...
```
