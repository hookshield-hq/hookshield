# Performance

Goal (product spec): webhook senders get their `202` in **under 20 ms (p99)**, and HookShield adds as little as possible to the trip to the destination.

## How to measure

`backend/tools/loadtest` sends webhooks to one endpoint and reports the latency senders see. With `-sink`, it also runs the destination and measures **end to end** latency: from the moment a webhook is sent until the destination receives it, through ingest, processor and dispatcher.

```bash
cd backend
go run ./tools/loadtest -url http://localhost:8080/v1/ep_x -token hs_in_… -c 16 -d 30s -rps 200
go run ./tools/loadtest -url … -token … -rps 200 -sink 127.0.0.1:4300   # endpoint target_url = http://127.0.0.1:4300/
```

| Flag | Default | Meaning |
|---|---|---|
| `-c` | 16 | concurrent senders |
| `-d` | 20s | duration |
| `-rps` | 0 | total requests per second (0 = as fast as possible) |
| `-size` | 512 | approximate body size in bytes |
| `-sink` | — | run a destination on this address and measure end-to-end latency (the endpoint must target it) |
| `-drain` | 30s | with `-sink`: how long to wait for deliveries after sending |

For realistic numbers, raise the per-endpoint rate limit (`HS_INGEST_RATE_LIMIT=0`) and run the generator on another machine than the services. For a CPU-independent regression check of ingest alone, use `make test-perf` (the `TestLatencyBudget` test).

## Results

Measured on 2026-11-04 on a small machine: 2 vCPU and 7 GB of RAM. PostgreSQL 17, Redis 7, `api`, `ingest`, `worker`, the load generator and the destination all ran on the same machine, with the default worker settings (4 processors, 8 dispatchers). 16 senders, 512-byte JSON bodies, 20 s per run.

| Load | Ingest p50 | Ingest p90 | Ingest p99 | Ingest max | End-to-end p50 | End-to-end p99 | Delivered |
|---|---|---|---|---|---|---|---|
| 200 req/s | 1.7 ms | 4.0 ms | **15.9 ms** | 60 ms | 5.6 ms | 167 ms | 3,983 / 3,983, no duplicates |
| 500 req/s | 2.8 ms | 11.0 ms | 21.3 ms | 49 ms | backlog | backlog | 9,687 / 9,687 |
| As fast as possible | 10.9 ms | 19.9 ms | 47.3 ms | 149 ms | — | — | 1,277 req/s accepted |

What this means:

- **Ingest meets the budget** (p99 < 20 ms) while the machine has headroom. Ingest work is one Redis round trip plus encryption. Above about 400 req/s on this 2-vCPU box, every process competes for the same two CPUs and the tail grows. Ingest itself accepted almost 1,300 req/s.
- **The worker is the throughput limit on one small machine:** about 300 to 400 deliveries per second here, mostly PostgreSQL work (store the log, record every attempt). Above that, webhooks queue in Redis and are delivered late but never lost: every one of them arrived, with no duplicates.
- **Scaling:** run more workers (`docker compose up -d --scale worker=N`). They share the queues through consumer groups. Raising `HS_PROCESSOR_CONCURRENCY` / `HS_DISPATCHER_CONCURRENCY` only helps when PostgreSQL and the CPU have room to spare. On a dedicated PostgreSQL host, expect several times these numbers.

### A bug the load test found

On a 2-CPU machine, raising the worker's concurrency made it almost stop (167 of 9,952 webhooks delivered in 50 s). Idle dispatchers wait on blocking Redis reads, and each one holds a pooled connection while it waits. The go-redis default pool (10 per CPU) was smaller than the number of waiting goroutines, so the processors waited seconds for a connection. The worker now sizes its pool for its blocking readers (`redisx.OpenFor`), and `TestBlockingReadersDoNotStarveThePool` guards against a regression. The default settings were also at risk on 1-vCPU servers.
