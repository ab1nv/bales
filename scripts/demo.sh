#!/bin/zsh
set -eu
cd "$(dirname "$0")/.."
make build >/dev/null
mkdir -p evidence/runs

typeset -a PIDS CURLS
cleanup() {
	for pid in $CURLS $PIDS; do
		kill $pid 2>/dev/null || true
	done
}
trap cleanup EXIT

./bin/bales-mockworker -addr 127.0.0.1:9001 -id mock-1 >/tmp/bales-mock-1.log 2>&1 &
PIDS+=($!)
./bin/bales-mockworker -addr 127.0.0.1:9002 -id mock-2 >/tmp/bales-mock-2.log 2>&1 &
PIDS+=($!)
./bin/bales-gateway -addr 127.0.0.1:8080 \
	-replicas http://127.0.0.1:9001,http://127.0.0.1:9002 \
	-telemetry evidence/runs/demo.csv >/tmp/bales-gateway.log 2>&1 &
PIDS+=($!)
sleep 1

python3 - <<'PY' >/tmp/bales_req.json
import json
prompt = "You are a helpful assistant answering a technical question. " * 300
print(json.dumps({
    "model": "mock",
    "stream": True,
    "max_tokens": 12,
    "messages": [{"role": "user", "content": prompt}],
}))
PY

for i in 1 2 3 4; do
	curl -sN -H 'Content-Type: application/json' \
		--data @/tmp/bales_req.json \
		http://127.0.0.1:8080/v1/chat/completions >/tmp/bales_sse_$i.txt &
	CURLS+=($!)
	sleep 0.03
done
for pid in $CURLS; do
	wait $pid || true
done

echo "== /debug/stats =="
curl -s http://127.0.0.1:8080/debug/stats
echo
echo "== telemetry =="
tail -5 evidence/runs/demo.csv
echo
echo "SSE tail:"
tail -1 /tmp/bales_sse_1.txt
