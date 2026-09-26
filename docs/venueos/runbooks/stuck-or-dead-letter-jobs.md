# Stuck or Dead-Letter Jobs

## Symptoms and Impact

Investigate when `service_worker_queue_oldest_age_seconds` exceeds the job-class
SLO, queue depth grows without successful completions, dead-letter counters rise,
or workers repeatedly log claim/state-transition failures. Impact depends on the
type: payment, refund, ticket, and entry-supporting work is customer- or
financially critical; bulk delivery and exports can usually tolerate a longer lag.

## Immediate Containment

1. Identify the affected registered job type and whether database health, a recent
   deployment, or an external provider is failing.
2. Stop producing more optional bulk work if it competes with a critical queue.
3. Roll back or disable the faulty handler/provider integration when evidence ties
   the incident to a recent release. Do not edit job rows directly.
4. Keep at least one healthy worker running unless execution itself is corrupting
   durable business state. A stopped worker leaves leases to expire safely.

## Diagnosis

- Compare queue depth, oldest due age, run result/class, worker process health, and
  database pool/lock metrics around the first alert.
- Search structured logs by job type and transition, then use job ID only in the
  restricted log system. Logs and traces intentionally omit payloads.
- Confirm worker and migration versions are compatible and migration `00003` is
  applied.
- Inspect job state, attempt count, `run_at`, lease owner/expiry, and the bounded
  safe error fields using read-only database access. Never retrieve or paste raw
  customer/provider payloads into incident chat.
- For leased rows, distinguish active work from an expired lease. Expired final
  attempts are moved to `dead_letter` by the sweep before each claim cycle.

## Safe Remediation

- Restore database/provider health or deploy the corrected idempotent handler.
- Let non-final expired leases be reclaimed automatically.
- Replay dead letters only through the future audited operator workflow after the
  cause is fixed. Until that workflow exists, escalate rather than issuing direct
  `UPDATE` statements.
- If an unsupported type/schema appears, deploy the matching reviewed handler or
  correct the producer; do not relabel the stored record.

## Verification and Escalation

Verify successful completions resume, oldest due age and depth fall, no new dead
letters appear, and the relevant business invariant/reconciliation check passes.
Escalate immediately for payments, refunds, oversell risk, ticket admission impact,
cross-tenant symptoms, or any evidence of sensitive data in errors. Record the
timeline, affected types/counts, safe remediation, reconciliation evidence, and an
owned follow-up test/alert improvement.
