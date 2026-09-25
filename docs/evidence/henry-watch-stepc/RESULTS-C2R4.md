# C2 round 4: Henry's stranded-intent finding (ah6s2o3)

Generated 2026-09-25T05:22:44Z. NOT INSTALLED. Live adapter unchanged: sha256 8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c

candidate sha256 56e5bc351abbeb3f5193750ab56014154bb82bb058e0cd656c3fa4789abc4881
suite sha256 6ab606cff01a6dec6ba27f2b25a5dffddf6409310bf0967e265f78ae575f8974 — 30 tests

## The finding
watch_c.py:461-469,485-493 — mail accepted, then a crash before the delivered mark, leaves stage=intent. If the source then leaves the poll window, that intent was never reconciled and the receipt ACK was stranded forever. The exact-ID inbox parser was correct; the missing piece was a recovery scan.

## The repair
1. The ACK tuple is now persisted BEFORE the mail, at intent time, so a crash leaves enough to ACK later without the source.
2. A per-tick RECOVERY SCAN walks every persisted intent INDEPENDENTLY of the current poll window and reconciles it by exact message id against the inbox: found -> delivered (the ACK pass then retries); absent -> intent retained and NOT ACKed; an unreadable inbox is treated as neither (WARN, retry next tick), because a failed read is not an absence.

## RED against the installed adapter
```
ERROR: test_quarantine_records_carry_no_payload
ERROR: test_quarantine_survives_restart_and_is_bounded
ERROR: test_application_error_envelope_does_not_count_as_a_sent_ack
ERROR: test_reconcile_requires_an_exact_id_match
ERROR: test_saturated_window_records_a_visible_gap_and_never_claims_drained
ERROR: test_ack_response_must_carry_the_posted_identity
ERROR: test_missing_recipient_is_not_actionable
ERROR: test_quarantine_write_failure_fails_closed
ERROR: test_absent_from_inbox_keeps_intent_and_does_not_ack
ERROR: test_intent_is_reconciled_after_the_source_leaves_the_window
ERROR: test_corrupt_state_is_not_read_as_first_run
FAIL: test_foreign_recipient_has_no_effect_even_from_henry
FAIL: test_only_allowlisted_kinds_become_actionable
FAIL: test_quarantine_is_never_dispatched_on_a_later_tick
FAIL: test_status_and_ack_do_not_create_work_or_ack_loops
FAIL: test_unknown_identity_is_quarantined_not_delivered
FAIL: test_unsupported_kind_from_a_trusted_sender_is_quarantined
FAIL: test_pending_ack_survives_the_message_leaving_the_poll_window
FAIL: test_poll_envelope_with_errors_is_not_processed
FAIL: test_saturated_window_blocks_delivery_and_holds_the_cursor
FAIL: test_failed_ack_is_not_recorded_as_sent_and_retries
FAIL: test_receipt_ack_is_posted_after_persistence
FAIL: test_restart_replay_does_not_duplicate_the_inbox_write
Ran 30 tests in 0.032s
FAILED (failures=12, errors=11)
```

## GREEN on the candidate
```
Ran 30 tests in 0.029s

OK
```

## Sabotage controls
```
$ remove the recovery scan                 -> FAILED (failures=1)
$ recover without checking the inbox       -> FAILED (failures=1)
$ persist the ACK tuple after the mail     -> FAILED (failures=1)
```

## Correction to RESULTS-C2R3, per Henry
I reported the gap sabotage as 1 failure. Henry's reproduction shows TWO. His count is right: the saturation sabotage fails both the gap-record assertion and the cursor-hold assertion, and I reported only the tail line of my own run.

## Test-invocation fix (Henry, p1910gg)

Henry found that `python3 test_stepc.py` discovered only 10 of 30 tests, because unittest.main() sat mid-file, before the later classes. A reviewer running it the obvious way saw '10/10 PASS' and a complete-looking suite.

FIXED: the runner block moved to the end of the file. Verified, same file, three invocations:
```
before, direct:  Ran 10 tests
after,  direct:  Ran 30 tests in 0.027s
after,  discover: Ran 30 tests in 0.027s
after,  module:   Ran 30 tests in 0.026s
```
This was a probe that silently under-reported its own population: the count was wrong in the reassuring direction, and only an external reviewer running it a different way could see it.
