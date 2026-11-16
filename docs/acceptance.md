# Acceptance — SRS Appendix A walk-through (Z4 step 3)

Every source-spec requirement from [SRS Appendix A](SRS.md#appendix-a--requirements-traceability-matrix), with what proves it. Any row that ends ❌ blocks the release.

**Status key**
- ✅ **test**: proven by an automated test that runs in CI. ✅ **live**: a live check has passed (see Evidence).
- 🔶 **live**: needs the running stack. Proven by the named demo script or check during the Z1 live run, the Z2 double run or the Z4 rehearsal. Write the date and result in the Evidence column.
- 📄 **doc**: proven by documentation; checked in the clean-machine rehearsal.

Go tests are `package.TestName`; extension tests are `file › test`.

| # | Requirement (source spec) | Proof | Status | Evidence |
|---|---|---|---|---|
| 1 | Video → summarised notes + real frames, published to Notion | `happy.sh` on test videos #1–#3 (`docs/test-videos.md`) | ✅ live | 2026-11-16: `happy.sh` on video #1 `COMPLETED`, 8/8 frames, page published |
| 2 | Driven entirely from the extension | Z1 step 5 (Take Notes → View in Notion); `jobs.test.ts › takeNotes …` | 🔶 live | |
| 3 | Queue-backed worker pool | `worker.TestHappyPathFakeStages`, `queue.TestPublishConfirmed` | ✅ test | |
| 4 | Respect every external API's rate limits | `ratelimit.TestSharedAcrossClients`, `TestBucketRate`, `TestDailyCap`, `TestNotionTemplate`; `notion.TestLimiterBucketPerToken`; `llm.TestGuardedFallbackAndBuckets`; live: `burst.sh` | ✅ test + 🔶 live | |
| 5 | Four separate containers; kill or scale one without touching others | `deploy/docker-compose.yml`; `scale.sh 3`, `kill-worker.sh` | 🔶 live | |
| 6 | Gateway accepts jobs and reports status, does no pipeline work | `api.TestPostJobNoExternalCalls`, `TestPostJobAccepted` | ✅ test | |
| 7 | Durable queue, at-least-once, redelivery on crash | `queue.TestUnackedRedeliveredOnConnClose`, `TestReconnectAfterForcedClose`, `TestDeclareTwice`; `worker.TestCrashRecovery` | ✅ test | |
| 8 | ≥ 2 independent workers | Compose `deploy.replicas: 2`; `health.sh` (`workers_alive ≥ 2`) | ✅ live | 2026-11-13: `health.sh` PASS, `workers_alive=2` |
| 9 | Take Notes button injected on watch pages | `button.test.ts › ensureButton is idempotent and requires a toolbar` | ✅ test | |
| 10 | Click captures the current video ID → background | `button.test.ts › reads the current ID at click time …` | ✅ test | |
| 11 | Keeps working across video-to-video navigation | `button.test.ts › reattaches once when a toolbar or the entire player is replaced`, `› ignores a late acknowledgment after navigating …` | ✅ test | |
| 12 | One-time Notion OAuth, token stored, no re-prompt | `auth.test.ts › connectWithOAuth …`, `› rejects an OAuth response with an unrelated state`; `api.TestExchangeOK`, `TestExchangeRejectsForeignRedirect`; live: Mode B in Z4 step 2 | ✅ test + 🔶 live | |
| 13 | Background submits jobs and relays status | `jobs.test.ts › takeNotes uses a fresh idempotency key per click …`; `poller.test.ts › pollOnce saves progress …` | ✅ test | |
| 14 | Popup shows Queued → … → Done | `JobCard.test.tsx › StepTracker highlights the current step …` | ✅ test | |
| 15 | One-click "View in Notion" | Z1 step 5 | 🔶 live | |
| 16 | Failures and warnings shown clearly, never raw JSON | `shared.test.ts › errorMessage handles unknown codes`, `› summarizeWarnings …`; `publish.TestWarningCalloutPlainWords` | ✅ test | |
| 17 | Timestamped transcript, true duration, Constraint A | `transcript.TestParseJSON3Manual`, `TestCleanAutoCaptions`, `TestTooLongRejectedBeforeCaptions`, `TestNoCaptions`; `youtube.TestMetadataParses` | ✅ test | |
| 18 | LLM summary bullets + timestamps of key visuals | `synthesis.TestStageShortVideoAndRepair`, `TestValidateDropsBadVisuals` | ✅ test | |
| 19 | Each flagged timestamp has a caption (what + why) | `synthesis.TestValidateDropsBadVisuals`; `pipeline.TestSynthesisJSONTags` | ✅ test | |
| 20 | Chunk long transcripts, merge in order | `synthesis.TestChunkSplitsWithOverlap`, `TestMergeKeepsOrder`, `TestStageResumesFromChunkCheckpoint` | ✅ test | |
| 21 | Seek to each timestamp, capture one frame from the stream | `frames.TestCaptureRealFrame`, `TestChooseTime`, `TestSceneChangeDetected`; `youtube.TestStreamURL` | ✅ test | |
| 22 | Upload frames; page with headings, bullets, captioned images | `publish.TestBlocksOrderAndCaptions`, `TestUploadsReusedOrRenewed`; `notion.TestUploadImageTwoStep`, `TestAppendBatches`; live: `happy.sh` | ✅ test + ✅ live | 2026-11-16: page with headings, bullets and 8 captioned frames published (video #1) |
| 23 | Status set with the stated meanings | `pipeline.TestStatusOrder`; `jobs.TestAllowedTransitions` | ✅ test | |
| 24 | Transition table | `jobs.TestAllowedTransitions`, `TestTransition`, `TestFailFromTerminalRejected` | ✅ test | |
| 25 | `POST /jobs` shape, 202 + `Location` | `api.TestPostJobAccepted` | ✅ test | |
| 26 | Validates the URL, returns immediately, no pipeline work | `api.TestPostJobErrors`, `TestParseVideoID`, `TestPostJobNoExternalCalls` | ✅ test | |
| 27 | Invalid input → 4xx with `{"error":{code,message}}` | `api.TestPostJobErrors`, `TestUnknownRouteAndMethod` | ✅ test | |
| 28 | Notion token per submission, never stored | `api.TestNoTokenInLogs`; `crypto.TestSealRoundTrip`; `worker.TestNotionTokenOnlyForPublishing`; `logging.TestRedacts` | ✅ test | |
| 29 | Omitted parent → default destination, stated in the README | `publish.TestResolveDestinations`; README §4 | ✅ test + 📄 doc | |
| 30 | `GET /jobs/{id}` shape | `api.TestGetJob`; `jobs.TestWarningsNeverNull` | ✅ test | |
| 31 | Polling is read-only; unknown ID → 404 | `jobs.TestGetJobViewIsReadOnly`; `api.TestGetJobUnknown` | ✅ test | |
| 32 | `GET /jobs/{id}/results` shape | `api.TestResultsCompleted`, `TestResultsFailed` | ✅ test | |
| 33 | Results before terminal → 409 | `api.TestResultsBeforeTerminal`; `shared.test.ts › getResults maps 409 to NOT_FINISHED` | ✅ test | |
| 34 | Success returns a working absolute Notion URL | `happy.sh` (open the printed URL) | ✅ live | 2026-11-16: `happy.sh` returned a working Notion URL (video #1) |
| 35 | 429s from any API re-queued with backoff; user sees waiting | `worker.TestRateLimitedDoesNotSpendFailureBudget`; `ratelimit.TestMaxWaitReturnsRateLimited`; `frames.TestThrottleDuringVerificationRetry`; `JobCard.test.tsx` (countdown); live: `force-429.sh llm\|notion\|youtube` | ✅ test + 🔶 live | |
| 36 | Worker crash → reassigned, no job lost or duplicated on Notion | `worker.TestCrashRecovery`, `TestDuplicateDeliveryDefers`; `jobs.TestClaimDeadOwnerImmediately`; `publish.TestAdoptsAfterCrashWindow`; live: `kill-worker.sh` | ✅ test + 🔶 live | |
| 37 | Ack only after durable completion; idempotent redelivery | `jobs.TestCompleteStageAtomic`; `publish.TestContentConvergesAfterPartialWrite`; `outbox.TestRelayNoDoublePublishConcurrent` | ✅ test | |
| 38 | Partial frame failure → text + good frames, `COMPLETED_WITH_WARNINGS` | `frames.TestCaptureFailures`; `jobs.TestPublishCompletesWithWarnings`; live: `frames-fail.sh` | ✅ test + 🔶 live | |
| 39 | All frames fail → text-only page | `publish.TestTextOnlyAndFrameGuards`; `faults.TestFrameFailAll`; live: `frames-fail.sh` | ✅ test + 🔶 live | |
| 40 | DLQ after bounded, configurable attempts; quick to demonstrate | `worker.TestDeadLetterAfterBudget`; `queue.TestDeliveryLimitDeadLetters`; `dlqwatch.TestWatcherMarksDeliveryLimit`; `api.TestListDLQ`, `TestReplayRequeues`; live: `dlq.sh` | ✅ test + ✅ live | 2026-11-13: `dlq.sh` PASS with an unavailable video ID, `DEAD_LETTERED` after 56 s (limit 60 s; tight margin) |
| 41 | ≤ 20 min supported, longer rejected deterministically | `transcript.TestTooLongRejectedBeforeCaptions`; `button.test.ts › precheckDuration rejects long videos inline …`; live: `too-long.sh` | ✅ test + ✅ live | 2026-11-13: `too-long.sh` PASS on a 61-min lecture (6FkRvTtUc-o), `FAILED VIDEO_TOO_LONG` 4 s after submit |
| 42 | Every image is a real frame at the identified time; never generated or substituted | `frames.TestCaptureRealFrame`, `TestDriftNeverExceeded`; `publish.TestTextOnlyAndFrameGuards` | ✅ test | |
| 43 | Every frame paired with an LLM caption | `publish.TestBlocksOrderAndCaptions`, `TestTextOnlyAndFrameGuards`; `frames.TestVerifyBatches` | ✅ test | |
| 44 | Unpacked extension, one documented build command | CI `extension` job (`npm ci && npm run build`); README §3; release zip | ✅ test + 📄 doc | |
| 45 | One compose file; `--scale worker=N`; stop/start containers | `scale.sh N`; `kill-worker.sh` | 🔶 live | 2026-11-13: `scale.sh 3` and `scale.sh 2` PASS; `kill-worker.sh` pending |
| 46 | README: base URL, default destination, every env var | README §2, §4, §5 | 📄 doc | |
| 47 | Grader can run the whole stack from the README alone | Z4 steps 1–2 (≤ 15 min, Quick and Full OAuth modes) | 🔶 live | |
| 48 | Health check of gateway, broker and state store | `health.TestCheckAllOK`, `TestCheckOneDown`; `api.TestHandler200`, `TestHandler503`; live: `health.sh` | ✅ test + 🔶 live | |
| 49 | Client experience | Extension tests above; Z4 rehearsal | 🔶 live | |
| 50 | End-to-end latency near the 20-min cap | `happy.sh` on video #3, time recorded in `docs/test-videos.md` (target ≤ 90 s) | 🔶 live | 2026-11-16: 80 s end to end on video #1 (5:55); still to time video #3 (17:52) with frames |
| 51 | Burst drains without LLM 429s; each click an independent job | `jobs.TestCreateJobIdempotencyKey`, `TestTwoJobsSameVideo`; `worker.TestTwoJobsSameVideoIndependent`; `jobs.test.ts › … fresh idempotency key per click`; live: `burst.sh` | ✅ test + 🔶 live | |
| 52 | Recovery time and correctness after a worker kill | `worker.TestCrashRecovery`; live: `kill-worker.sh` (≤ 15 s, one page) | ✅ test + 🔶 live | |
| 53 | Constraints A and B hold on held-out videos (LLM-as-judge) | Rows 41–43; live: inspect pages for videos #1–#3 | 🔶 live | |
| 54 | Justify broker, state model, rate-limit coordination, topology, methods, tooling | README §9; SRS §19 | 📄 doc | |

## Open items to close before tagging v1.0.0

- [ ] All 🔶 rows have a dated PASS in the Evidence column (Z1 live run, Z2 double run, Z4 rehearsal).
- [ ] #38 item 1: publish once to a Notion **database** destination.
- [ ] Clean-machine rehearsal ≤ 15 min in Quick mode and in Full OAuth mode (Z4 steps 1–2), by the teammate who did not write the README.
