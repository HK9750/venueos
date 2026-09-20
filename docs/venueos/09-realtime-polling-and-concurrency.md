# Realtime, Polling, and Concurrency

## Goals

Live availability and entry dashboards should update quickly, but correctness must
not depend on a persistent connection. The client recovery model is always:

```text
authoritative snapshot -> ordered deltas -> detect gap -> fresh snapshot
```

PostgreSQL owns inventory and a bounded replay log. Redis distributes committed
events between API replicas. WebSockets provide low-latency delivery. Conditional
HTTP polling is the universal fallback.

## Availability Versions and Event Creation

Every session has a monotonically increasing inventory revision. A transaction that
changes visible availability:

1. locks/updates inventory;
2. increments the session revision once for the logical mutation;
3. inserts one or more `realtime_events` carrying that committed revision;
4. inserts outbox notification for fan-out;
5. commits atomically.

Consumers never see uncommitted changes. A relay publishes the event to a Redis
channel keyed by environment/organization/session. The durable replay row permits
recovery when Redis or a WebSocket replica loses a message.

## Snapshot Endpoint

`GET /v1/public/sessions/{id}/availability` returns:

- session and map/pool identifiers;
- current `revision` and replay `cursor`;
- `server_time` and hold policy;
- safe seat/pool availability and public price references;
- `ETag` derived from session/revision/audience variant;
- recommended poll delay and optional realtime topic metadata.

`If-None-Match` returns `304` without a body when unchanged. Responses may be
compressed and cached briefly only when cache keys separate session, channel,
audience, locale, and any other material variant. Private hold ownership is returned
through the hold endpoint, not leaked into public snapshots.

## Polling Delta Endpoint

`GET .../availability/changes?after={cursor}&limit={n}` returns ordered events and a
new cursor. Long polling may wait up to a bounded server duration (for example
20–25 seconds) and still respects request cancellation and server drain.

- Cursor encodes session/topic and sequence; it is signed/opaque.
- If no events occur, return empty list with current cursor.
- If the cursor is older than retained history, mismatched, or a gap is detected,
  return `409 replay_unavailable` with instruction to fetch a snapshot.
- Limit and response bytes are bounded. If more events remain, `has_more=true` and
  client immediately requests the next page.
- Client uses jittered interval/backoff, pauses when hidden where appropriate, and
  refreshes snapshot after repeated errors.

## WebSocket Protocol

### Connection

1. Client obtains one-time ticket from authenticated/rate-limited HTTPS endpoint.
2. Ticket contains principal/audience, allowed topics, issue/expiry, unique ID, and
   optional last cursor; Redis enforces single use.
3. Client connects to `/v1/realtime?ticket=...`; only the short-lived one-time token
   is in the URL.
4. Server validates origin, consumes ticket, enforces connection limits, and sends
   `hello` with heartbeat interval, maximum payload, and server time.
5. Client subscribes to permitted topic and supplies last known cursor/revision.

### Messages

```json
{
  "type": "availability.changed",
  "event_id": "evt_...",
  "topic": "session:...:availability",
  "sequence": 481,
  "occurred_at": "2026-09-20T10:00:00Z",
  "schema_version": 1,
  "data": {"revision": 481, "seats": [], "pools": []}
}
```

Control messages include `hello`, `subscribe`, `subscribed`, `ping`, `pong`,
`resync_required`, `server_draining`, `error`, and `unsubscribe`. Business messages
contain minimal deltas and no customer/hold identity.

### Liveness and Limits

- Server sends ping and closes peers missing bounded pong/read deadline.
- Per-connection write queue, topics, subscriptions, incoming message rate, and
  payload size are capped.
- One writer goroutine owns the socket. Readers only validate/enqueue commands.
- Slow consumers are disconnected with `resync_required`; memory is never allowed
  to grow without bound.
- Limits apply by IP, principal, organization, session, and replica. Public hot
  sessions use admission control so sockets cannot exhaust API capacity.
- During shutdown, stop upgrades, send drain message, allow brief reconnect window,
  then close. Clients reconnect with exponential backoff and jitter.

## Reconnect and Replay

On reconnect, the client supplies last cursor. The server reads `realtime_events`
after the cursor up to bounds and then joins live fan-out without a race by using a
high-water mark/subscription handoff. If history is unavailable or any sequence gap
exists, send `resync_required`; client fetches a fresh snapshot before applying more
deltas.

Clients apply only strictly increasing revisions. Duplicate event IDs/sequences are
ignored. An event beyond the next expected sequence triggers resync, not speculative
application.

## Redis Failure

Redis Pub/Sub is lossy by design. During outage:

- database writes and replay rows continue;
- API snapshot and polling delta endpoints continue from PostgreSQL;
- existing sockets may receive a degraded/resync message or close;
- reconnect uses polling/replay until Redis is healthy;
- no code treats Redis cache/counter/pubsub state as inventory authority.

If Redis is unavailable for one-time socket ticket consumption, new socket upgrades
can fail closed while HTTP polling remains available. Rate-limit fallback behavior
is endpoint-specific and bounded to protect service.

## Concurrency Matrix

| Operation | Coordination | Expected losing result |
|---|---|---|
| Hold same reserved seat | Stable-order row locks | `409 inventory_unavailable` |
| Hold final GA units | Conditional counter update | `409 insufficient_inventory` |
| Modify same hold | Row lock + expected version | `412 version_conflict` |
| Expire vs confirm hold | Lock hold then inventory in common order | Exactly one terminal transition; late-payment policy if applicable |
| Duplicate checkout | Idempotency record + order uniqueness | Original response replay |
| Webhook vs client status refresh | Inbox uniqueness + monotonic transition | Same final state |
| Two refunds | Lock order/refund totals | Second rejected/adjusted before excess |
| Two scans | Ticket row lock + ticket/admission unique keys | One admitted, one already admitted |
| Promo final redemption | Promotion counter/unique constraint | Limit reached |
| Waitlist offer workers | Queue lock with `SKIP LOCKED` + offer uniqueness | One candidate/offer per eligible slot |
| Job workers | Lease + `SKIP LOCKED` + dedupe key | One active claimant; retry after lease expiry |

All multi-row locks define order in code comments and tests. Network calls are never
made while a row lock is held.

## Hot-Session Protection

- Availability snapshots use compact projections and conditional responses.
- Collapse identical cache fills/request bursts (singleflight locally) without
  caching tenant-private data incorrectly.
- Rate-limit aggressive clients and publish recommended poll cadence.
- Batch changes into one logical revision when semantically safe.
- Cap hold quantity and per-principal active holds.
- Database pools reserve capacity so availability traffic cannot starve checkout,
  webhooks, or scans; API and worker pool budgets are separate.
- Shed optional reports/exports before booking and entry traffic.

## Telemetry

Measure active/accepted/rejected sockets, connections per session, message and byte
rates, queue depth, slow-consumer disconnects, heartbeat timeouts, replay count,
replay-unavailable count, snapshot 304 ratio, polling QPS/duration, Redis publish
lag/failure, event commit-to-client lag, and resync frequency. Never use unbounded
session/organization IDs as metric labels; put them in controlled logs/traces.

## Tests

- Event revision and replay row commit atomically with inventory.
- Publish after commit; no rollback event leaks.
- Disconnect/reconnect with duplicates, missing event, old cursor, and live handoff.
- Redis outage and recovery with polling continuity.
- Slow reader and oversized/incoming message attacks remain bounded.
- Thousands of synthetic connections under reference hardware meet memory/CPU and
  delivery-lag budgets.
- Race tests verify each concurrency-matrix outcome and run repeatedly under the Go
  race detector where applicable.

