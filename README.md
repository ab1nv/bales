# Bales

<p align="center">
  <img src="assets/logo.png" alt="Bales logo" width="180" />
</p>

Bales is a prototype LLM gateway that schedules mixed streaming and batch traffic
across replicas. It values cached prefixes with a calibrated prefill cost model,
coalesces duplicate in-flight prompts, and orders requests by virtual deadlines.

The `proto` branch contains the v2 rewrite in Go. The `main` branch holds the
earlier CPU inference gateway.

Build and test with `make build` and `make test`. Full documentation follows as
the prototype stabilizes.
