package sim

import (
	"fmt"
	"math"
	"math/rand"
)

const (
	// BlockTokens is the trace block size used by the generators.
	BlockTokens = 512
	// hotGroup and warmGroup are shared prefixes used by load traces.
	hotGroup  = "hot"
	warmGroup = "warm"
)

func chainFor(group string, prefixBlocks, suffixBlocks, unique int) []uint64 {
	out := make([]uint64, 0, prefixBlocks+suffixBlocks)
	base := hashString(group)
	for i := range prefixBlocks {
		out = append(out, mix64(base+uint64(i)*0x9e3779b97f4a7c15))
	}
	u := hashString(fmt.Sprintf("%s-%d", group, unique))
	for j := range suffixBlocks {
		out = append(out, mix64(u+uint64(j)*0x9e3779b97f4a7c15))
	}
	return out
}

func hashString(s string) uint64 {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	h := uint64(offset)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// BurstTrace builds a cold same-prompt burst.
func BurstTrace(n, promptTokens, outputTokens int, staggerMS float64) Trace {
	blocks := max(1, (promptTokens+BlockTokens-1)/BlockTokens)
	chain := chainFor("burst", blocks, 0, 0)
	tr := Trace{Name: "burst"}
	for i := range n {
		tr.Requests = append(tr.Requests, Request{
			ID:           fmt.Sprintf("burst-%d", i),
			ArrivalMS:    float64(i) * staggerMS,
			PromptTokens: blocks * BlockTokens,
			OutputTokens: outputTokens,
			Group:        "burst",
			Chain:        chain,
			Stream:       true,
		})
	}
	tr.DurationMS = float64(max(n-1, 0))*staggerMS + 3000
	return tr
}

// LoadTrace generates Poisson arrivals with hot, warm and unique prefixes.
func LoadTrace(rate, durationMS float64, seed int64) Trace {
	rng := rand.New(rand.NewSource(seed))
	tr := Trace{Name: "load"}
	t := 0.0
	for i := 0; ; i++ {
		t += rng.ExpFloat64() / rate * 1000
		if t > durationMS {
			break
		}
		group := hotGroup
		prefix := 4
		suffix := 1
		r := rng.Float64()
		switch {
		case r < 0.6:
			// hot: 2048-token shared prefix plus a unique 512-token suffix
		case r < 0.85:
			group = warmGroup
			prefix = 2
		default:
			group = fmt.Sprintf("unique-%d", i)
			prefix = 0
			suffix = 3
		}
		chain := chainFor(group, prefix, suffix, i)
		stream := rng.Float64() < 0.85
		out := 32
		if !stream {
			out = 128
		}
		tr.Requests = append(tr.Requests, Request{
			ID:           fmt.Sprintf("load-%d", i),
			ArrivalMS:    t,
			PromptTokens: (prefix + suffix) * BlockTokens,
			OutputTokens: out,
			Group:        group,
			Chain:        chain,
			Stream:       stream,
		})
	}
	tr.DurationMS = durationMS
	return tr
}

// LongContextTrace shares an 8k-token prefix across requests.
func LongContextTrace(rate, durationMS float64, seed int64) Trace {
	rng := rand.New(rand.NewSource(seed))
	tr := Trace{Name: "longctx"}
	t := 0.0
	for i := 0; ; i++ {
		t += rng.ExpFloat64() / rate * 1000
		if t > durationMS {
			break
		}
		group := hotGroup
		prefix := 16
		if rng.Float64() > 0.7 {
			group = warmGroup
			prefix = 8
		}
		chain := chainFor(group, prefix, 1, i)
		tr.Requests = append(tr.Requests, Request{
			ID:           fmt.Sprintf("long-%d", i),
			ArrivalMS:    t,
			PromptTokens: (prefix + 1) * BlockTokens,
			OutputTokens: 24,
			Group:        group,
			Chain:        chain,
			Stream:       true,
		})
	}
	tr.DurationMS = durationMS
	return tr
}

// AffinityTrace varies prefix concentration and length.
func AffinityTrace(name string, seed int64, prefixBlocks int, hotShare float64) Trace {
	rng := rand.New(rand.NewSource(seed))
	const duration = 6000.0
	const rate = 10.0
	tr := Trace{Name: name}
	t := 0.0
	for i := 0; ; i++ {
		t += rng.ExpFloat64() / rate * 1000
		if t > duration {
			break
		}
		group := fmt.Sprintf("cold-%d", i)
		prefix := 0
		if rng.Float64() < hotShare {
			group = "hot"
			prefix = prefixBlocks
		}
		chain := chainFor(group, prefix, 2, i)
		tr.Requests = append(tr.Requests, Request{
			ID:           fmt.Sprintf("%s-%d", name, i),
			ArrivalMS:    t,
			PromptTokens: (prefix + 2) * BlockTokens,
			OutputTokens: 32,
			Group:        group,
			Chain:        chain,
			Stream:       true,
		})
	}
	tr.DurationMS = duration
	return tr
}

// OfferedRate is a helper for sweeps.
func OfferedRate(rate float64) float64 { return math.Round(rate*10) / 10 }
