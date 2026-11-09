# Design decisions log

Source: docs/SRS.md section 19.

- Language: Go. Why: concurrency primitives, small static binaries. Rejected: Python, Node.
- Broker: RabbitMQ (quorum work queue). Why: native acks, redelivery, DLX, TTL delayed retry, delivery limits. Rejected: Redis Streams, NATS JetStream.
- Job state: Postgres with checkpoints + events. Why: atomic result+status transactions, durable resume. Rejected: Redis, state in messages.
- Enqueue: transactional outbox. Why: no lost jobs if broker is down or gateway crashes after commit. Rejected: publish-then-commit.
- Global LLM limit: Redis token buckets via Lua, ~80% of provider limit. Why: atomic, cross-process, prevents 429s. Rejected: per-worker limits, broker-level throttling.
- Retry: per-delay TTL queues + DLX. Why: exponential backoff without a scheduler or head-of-line blocking. Rejected: delayed-message plugin, sleeping in worker.
- Crash safety: ack after durable commit; leases + Redis heartbeats; idempotent stages. Why: at-least-once is safe only if redelivery is harmless. Rejected: ack-on-receive.
- Topology: one worker type, all stages. Why: simplest scaling; checkpoints give per-stage resume. Rejected: per-stage workers (future scope).
- Transcript: yt-dlp captions (json3). Why: free, timestamped, gives duration. Rejected: YouTube Data API, Whisper.
- Frames: ffmpeg seek on stream URL + scene snap + sharpness. Why: real frames only, no full download. Rejected: full download, thumbnails.
- LLM: Gemini Flash free tier behind an interface. Why: $0, image support, tight limits showcase the limiter. Rejected: paid models, local only.
- Token handling: passed per job, AES-GCM encrypted in broker, never stored. Why: matches spec, safe at rest. Rejected: plaintext in message, server-side token storage.
