// Command simulate runs trace-driven routing and queue experiments.
package main

import (
	"flag"
	"fmt"
	"log"

	"github.com/ab1nv/bales/internal/sim"
)

func main() {
	var (
		exp      = flag.String("exp", "all", "experiment: all, burst, load, longctx, affinity, ablation, queue")
		out      = flag.String("out", "evidence/runs", "output directory")
		seeds    = flag.Int("seeds", 3, "number of random seeds")
		replicas = flag.Int("replicas", 4, "number of replicas")
	)
	flag.Parse()
	if err := sim.RunExperiment(*exp, *out, *seeds, *replicas); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("wrote %s experiment CSVs to %s\n", *exp, *out)
}
