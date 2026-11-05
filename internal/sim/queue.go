package sim

import (
	"math"
	"math/rand"
	"sort"
)

// QueueSummary is the output of the single-server queue experiment.
type QueueSummary struct {
	Streams      int
	Batches      int
	StreamP50    float64
	StreamP99    float64
	BatchMaxWait float64
	BatchP99Wait float64
	Goodput      float64
}

type queueJob struct {
	stream   bool
	arrival  float64
	work     float64
	deadline float64
	start    float64
}

// RunQueue simulates a non-preemptive single server with capacity one
// millisecond of work per millisecond. Policies are fifo, strict_priority, edf
// and wfq_stride. batchDeadlineMS sets the batch virtual deadline offset.
func RunQueue(rate, durationMS float64, seed int64, policy string, batchDeadlineMS float64) QueueSummary {
	if batchDeadlineMS <= 0 {
		batchDeadlineMS = 5000
	}
	rng := rand.New(rand.NewSource(seed))
	var jobs []*queueJob
	t := 0.0
	for {
		t += rng.ExpFloat64() / rate * 1000
		if t > durationMS {
			break
		}
		stream := rng.Float64() < 0.7
		work := 20 + rng.Float64()*60
		deadline := t + 300
		if !stream {
			work = 200 + rng.Float64()*400
			deadline = t + batchDeadlineMS
		}
		jobs = append(jobs, &queueJob{stream: stream, arrival: t, work: work, deadline: deadline})
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].arrival < jobs[j].arrival })

	var pending []*queueJob
	var cur *queueJob
	now := 0.0
	busyUntil := 0.0
	idx := 0
	addArrivals := func() {
		for idx < len(jobs) && jobs[idx].arrival <= now+1e-9 {
			pending = append(pending, jobs[idx])
			idx++
		}
	}
	for idx < len(jobs) || len(pending) > 0 || cur != nil {
		if cur == nil {
			addArrivals()
			if len(pending) == 0 {
				if idx >= len(jobs) {
					break
				}
				now = jobs[idx].arrival
				addArrivals()
			}
			cur = pickQueueJob(pending, policy)
			pending = removeQueueJob(pending, cur)
			cur.start = now
			busyUntil = now + cur.work
			continue
		}
		nextArrival := math.Inf(1)
		if idx < len(jobs) {
			nextArrival = jobs[idx].arrival
		}
		if nextArrival < busyUntil {
			now = nextArrival
			addArrivals()
			continue
		}
		now = busyUntil
		cur = nil
	}

	var streamTTFT, batchWait []float64
	for _, j := range jobs {
		wait := j.start - j.arrival
		if j.stream {
			streamTTFT = append(streamTTFT, wait+j.work)
		} else {
			batchWait = append(batchWait, wait)
		}
	}
	s := QueueSummary{
		Streams:      len(streamTTFT),
		Batches:      len(batchWait),
		StreamP50:    percentile(streamTTFT, 50),
		StreamP99:    percentile(streamTTFT, 99),
		BatchMaxWait: maxFloat(batchWait),
		BatchP99Wait: percentile(batchWait, 99),
	}
	var ok int
	for _, ttft := range streamTTFT {
		if ttft <= 500 {
			ok++
		}
	}
	if durationMS > 0 {
		s.Goodput = float64(ok) / (durationMS / 1000)
	}
	return s
}

func pickQueueJob(pending []*queueJob, policy string) *queueJob {
	if len(pending) == 0 {
		return nil
	}
	best := pending[0]
	less := func(a, b *queueJob) bool {
		switch policy {
		case "strict_priority":
			if a.stream != b.stream {
				return a.stream
			}
			return a.arrival < b.arrival
		case "edf":
			if a.deadline != b.deadline {
				return a.deadline < b.deadline
			}
			return a.arrival < b.arrival
		case "wfq_stride":
			wa, wb := 1.0, 1.0
			if a.stream {
				wa = 4
			}
			if b.stream {
				wb = 4
			}
			pa, pb := a.arrival/wa, b.arrival/wb
			if pa != pb {
				return pa < pb
			}
			return a.arrival < b.arrival
		default:
			return a.arrival < b.arrival
		}
	}
	for _, j := range pending[1:] {
		if less(j, best) {
			best = j
		}
	}
	return best
}

func removeQueueJob(pending []*queueJob, target *queueJob) []*queueJob {
	for i, j := range pending {
		if j == target {
			return append(pending[:i], pending[i+1:]...)
		}
	}
	return pending
}

func maxFloat(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x > m {
			m = x
		}
	}
	return m
}
