## 7. Results
**Deployed API, all response codes:** ![ECS](screenshots/response-codes-ecs.png)

**HttpUser vs FastHttpUser** (100 users, ramp 20/s, 85% GET / 5% GET-missing / 10% POST):
| Client | RPS | Median | p95 | Failures |
|---|---|---|---|---|
| HttpUser | 92.9 | 84 ms | 92 ms | 0 |
| FastHttpUser | 93.2 | 83 ms | 93 ms | 0 |

![HttpUser](screenshots/locust-httpuser.png) ![FastHttpUser](screenshots/locust-fasthttpuser.png)

**No real difference.** Locust is closed-loop: 100 users / (~1 s think time + ~0.085 s response) is about 92 RPS, so the users cap the rate, not the client. The ~84 ms latency is almost all network round trip from my laptop to us-west-2, so FastHttpUser's lower client overhead is invisible. It only matters when the load generator's CPU is the bottleneck.

**Stress test** (+50 users every 20 s up to 300, no think time): ![Stress](screenshots/locust-stress-charts.png)

**Most common operation:** reads (GET) dominate a store, so the in-memory map uses a `sync.RWMutex`, which lets many GETs proceed in parallel while POSTs take the exclusive lock.

**Grafana, idle vs loaded:** ![Grafana](screenshots/grafana-idle-vs-loaded.png)
Request log excerpt: [request-logs.txt](screenshots/request-logs.txt)

**Dashboard vs logs:** I use the dashboard to see *that* something is wrong right now and when it started (rate drops, error % rises, p95 climbs). I use the logs afterwards to see *which* requests failed and why, since every line records the endpoint, status, and duration.