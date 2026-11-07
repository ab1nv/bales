// Command calibrate fits the prefill cost model from a q,c,ms CSV.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/ab1nv/bales/internal/cost"
)

func main() {
	var (
		in  = flag.String("in", "", "CSV with q,c,ms rows (header optional)")
		out = flag.String("out", "", "optional JSON output for the fitted model")
	)
	flag.Parse()
	if *in == "" {
		log.Fatal("-in is required")
	}
	samples, err := readSamples(*in)
	if err != nil {
		log.Fatal(err)
	}
	model, stats, err := cost.Fit(samples)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("samples=%d RMSE=%.4f ms MAPE=%.3f%% R2=%.5f\n", stats.Samples, stats.RMSE, stats.MAPE, stats.R2)
	fmt.Printf("A=%.9g B=%.9g F=%.6g G=%.6g\n", model.A, model.B, model.F, model.G)
	if *out != "" {
		data, _ := json.MarshalIndent(model, "", "  ")
		if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
			log.Fatal(err)
		}
	}
}

func readSamples(path string) ([]cost.Sample, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, err
	}
	var out []cost.Sample
	for i, row := range rows {
		if len(row) < 3 {
			continue
		}
		q, err1 := strconv.Atoi(row[0])
		c, err2 := strconv.Atoi(row[1])
		ms, err3 := strconv.ParseFloat(row[2], 64)
		if err1 != nil || err2 != nil || err3 != nil {
			if i == 0 {
				continue // header
			}
			return nil, fmt.Errorf("row %d: expected q,c,ms, got %v", i+1, row)
		}
		out = append(out, cost.Sample{Q: q, C: c, MS: ms})
	}
	if len(out) < 4 {
		return nil, fmt.Errorf("need at least 4 samples, got %d", len(out))
	}
	return out, nil
}
