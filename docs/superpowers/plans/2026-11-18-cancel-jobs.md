# Cancel an in-progress note job

The user can stop one tracked job while queued, working, or waiting for retry. Cancellation becomes final when committed to PostgreSQL. Existing Notion pages are never deleted. The cutoff is the durable checkpoint immediately before the first create-page request, since a lost response cannot prove that Notion did not create a page.

1. Storage: represent CANCELLED as terminal; serialize cancellation with page creation and all status updates. Clear retry/wait and lease state, record one event, and make repeated cancels idempotent. Derive can_cancel from the same status and checkpoint rules, including existing rows.
2. Runtime: interrupt the active stage context when a cancellation commits. Late deliveries, stale leases and in-flight failures must not run another stage or schedule meaningful work for the cancelled job. Let delayed messages expire and acknowledge them on delivery.
3. Gateway: POST /jobs/{job_id}/cancel returns the current job view on success, 404 for an unknown job and 409 JOB_NOT_CANCELLABLE past the cutoff. Use the existing per-job UUID and origin policy. Results for CANCELLED have no page.
4. Extension: use the tracked gateway, show Cancel on eligible jobs including retry waits, disable it during the request, persist the server state and show a terminal cancellation. Handle stale polling responses and reject late cancellation with a clear explanation.
5. Verify with state-machine, database race, worker, route and popup tests. Run the repository Go suite, integration suite, lint, extension tests and build. Keep the feature branch separate from the active OAuth rehearsal.
