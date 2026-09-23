# Step C evidence: Henry to Mayor intake adapter (gt-henry-watch)

Generated 2026-09-23T05:21:45Z by the Gastown Mayor. NOT INSTALLED; the live adapter is unchanged at sha256 8f386008ac3c300dcf4bfe34820825b5634cf2471ceae0408f26ddb79a01e76c.

Files here: watch_c.py (candidate, sha256 ed155fe7b4a9b86fcfa8b3e085735043c2774f32ddcab594e09e0b9bcbe8faaa), test_stepc.py (suite, sha256 2ac4ba0a4ca6682974f8fd8f61b90f1ebc95ed0983a69971d68642302f3e98d8).

## 1. RED, run against the INSTALLED adapter

```
$ STEPC_TARGET=./red_target.py python3 -m unittest test_stepc -v   # red_target.py == the installed script
test_corrupt_state_is_not_read_as_first_run (test_stepc.TestIntake.test_corrupt_state_is_not_read_as_first_run) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:175: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-fclsy5dy/gt/.runtime/henry-watch.state' mode='w' encoding='utf-8'>
  open(self.m.STATE, "w").write("{ this is not json")
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-fclsy5dy/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ERROR
test_disk_write_failure_does_not_ack (test_stepc.TestIntake.test_disk_write_failure_does_not_ack) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-t9r7o66n/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:188: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-t9r7o66n/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  pass
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_duplicate_delivery_of_same_id_is_idempotent (test_stepc.TestIntake.test_duplicate_delivery_of_same_id_is_idempotent) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-qnrqn_8v/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z DELIVERED m1 2026-09-23T01:00:00.000Z :: Henry #dev: SUMMARY
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=1 new=1 last_seen=2026-09-23T01:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-qnrqn_8v/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z DELIVERED m2 2026-09-23T01:00:00.000Z :: Henry #dev: SUMMARY
2026-09-23T05:21:45Z OK window=2 oldest=2026-09-23T01:00:00.000Z henry=2 new=1 last_seen=2026-09-23T01:00:00.000Z
ok
test_failed_ack_is_not_recorded_as_sent_and_retries (test_stepc.TestIntake.test_failed_ack_is_not_recorded_as_sent_and_retries) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-mx30vjwb/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z DELIVERED m1 2026-09-23T01:00:00.000Z :: Henry #dev: SUMMARY
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=1 new=1 last_seen=2026-09-23T01:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-mx30vjwb/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=1 new=0 last_seen=2026-09-23T01:00:00.000Z
FAIL
test_foreign_recipient_creates_no_work (test_stepc.TestIntake.test_foreign_recipient_creates_no_work) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-xz2q2uou/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=0 new=0 last_seen=2026-09-23T00:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-xz2q2uou/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_idle_tick_makes_no_model_call (test_stepc.TestIntake.test_idle_tick_makes_no_model_call)
v2 qualification: no LLM inference when there is no work. ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-6wvzu7w3/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T08:00:00.000Z henry=1 new=0 last_seen=2026-09-23T09:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-6wvzu7w3/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_malformed_metadata_does_not_crash_and_creates_no_work (test_stepc.TestIntake.test_malformed_metadata_does_not_crash_and_creates_no_work) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-y8y993_j/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z OK window=3 oldest=2026-09-23T01:00:00.000Z henry=0 new=0 last_seen=2026-09-23T00:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-y8y993_j/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_own_post_is_never_ingested_ack_loop (test_stepc.TestIntake.test_own_post_is_never_ingested_ack_loop) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-5mjx0zwz/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=0 new=0 last_seen=2026-09-23T00:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-5mjx0zwz/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_receipt_ack_is_posted_after_persistence (test_stepc.TestIntake.test_receipt_ack_is_posted_after_persistence) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-zxckv2lp/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z DELIVERED m1 2026-09-23T01:00:00.000Z :: Henry #dev: SUMMARY
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=1 new=1 last_seen=2026-09-23T01:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-zxckv2lp/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
FAIL
test_restart_replay_does_not_duplicate_the_inbox_write (test_stepc.TestIntake.test_restart_replay_does_not_duplicate_the_inbox_write) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py:101: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-8ck7rqre/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:157: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-8ck7rqre/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  pass
ResourceWarning: Enable tracemalloc to get the object allocation traceback
2026-09-23T05:21:45Z DELIVERED m1 2026-09-23T01:00:00.000Z :: Henry #dev: SUMMARY
2026-09-23T05:21:45Z OK window=1 oldest=2026-09-23T01:00:00.000Z henry=1 new=1 last_seen=2026-09-23T01:00:00.000Z
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-8ck7rqre/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
FAIL

======================================================================
ERROR: test_corrupt_state_is_not_read_as_first_run (test_stepc.TestIntake.test_corrupt_state_is_not_read_as_first_run)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py", line 177, in test_corrupt_state_is_not_read_as_first_run
    rc = self.run_tick()
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py", line 85, in run_tick
    return self.m.main()
           ~~~~~~~~~~~^^
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py", line 179, in main
    last, seen = read_state()
                 ~~~~~~~~~~^^
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/red_target.py", line 103, in read_state
    d = json.loads(raw)
  File "/usr/lib/python3.13/json/__init__.py", line 346, in loads
    return _default_decoder.decode(s)
           ~~~~~~~~~~~~~~~~~~~~~~~^^^
  File "/usr/lib/python3.13/json/decoder.py", line 345, in decode
    obj, end = self.raw_decode(s, idx=_w(s, 0).end())
               ~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^
  File "/usr/lib/python3.13/json/decoder.py", line 361, in raw_decode
    obj, end = self.scan_once(s, idx)
               ~~~~~~~~~~~~~~^^^^^^^^
json.decoder.JSONDecodeError: Expecting property name enclosed in double quotes: line 1 column 3 (char 2)

======================================================================
FAIL: test_failed_ack_is_not_recorded_as_sent_and_retries (test_stepc.TestIntake.test_failed_ack_is_not_recorded_as_sent_and_retries)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py", line 142, in test_failed_ack_is_not_recorded_as_sent_and_retries
    self.assertEqual(self.posts, ["m1"], "a failed ACK must be retried on the next tick")
    ~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
AssertionError: Lists differ: [] != ['m1']

Second list contains 1 additional elements.
First extra element 0:
'm1'

- []
+ ['m1'] : a failed ACK must be retried on the next tick

======================================================================
FAIL: test_receipt_ack_is_posted_after_persistence (test_stepc.TestIntake.test_receipt_ack_is_posted_after_persistence)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py", line 131, in test_receipt_ack_is_posted_after_persistence
    self.assertTrue(hasattr(self.m, "post_ack"), "adapter must have a receipt-ACK path")
    ~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
AssertionError: False is not true : adapter must have a receipt-ACK path

======================================================================
FAIL: test_restart_replay_does_not_duplicate_the_inbox_write (test_stepc.TestIntake.test_restart_replay_does_not_duplicate_the_inbox_write)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py", line 161, in test_restart_replay_does_not_duplicate_the_inbox_write
    self.assertEqual(len(self.mail), n_after_crash,
    ~~~~~~~~~~~~~~~~^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                     "a crash between the inbox write and the state write must not "
                     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
                     "produce a second inbox write for the same message id")
                     ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
AssertionError: 2 != 1 : a crash between the inbox write and the state write must not produce a second inbox write for the same message id

----------------------------------------------------------------------
Ran 10 tests in 0.018s

FAILED (failures=3, errors=1)
```

## 2. GREEN, same suite against the candidate

```
$ python3 -m unittest test_stepc -v
test_corrupt_state_is_not_read_as_first_run (test_stepc.TestIntake.test_corrupt_state_is_not_read_as_first_run) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:175: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-4sidq6ov/gt/.runtime/henry-watch.state' mode='w' encoding='utf-8'>
  open(self.m.STATE, "w").write("{ this is not json")
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-4sidq6ov/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-4sidq6ov/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_disk_write_failure_does_not_ack (test_stepc.TestIntake.test_disk_write_failure_does_not_ack) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-gkn0e8pr/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:188: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-gkn0e8pr/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  pass
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_duplicate_delivery_of_same_id_is_idempotent (test_stepc.TestIntake.test_duplicate_delivery_of_same_id_is_idempotent) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-dipldxmp/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-dipldxmp/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_failed_ack_is_not_recorded_as_sent_and_retries (test_stepc.TestIntake.test_failed_ack_is_not_recorded_as_sent_and_retries) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-9mm_62jx/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-9mm_62jx/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_foreign_recipient_creates_no_work (test_stepc.TestIntake.test_foreign_recipient_creates_no_work) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-_6pup0oj/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-_6pup0oj/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_idle_tick_makes_no_model_call (test_stepc.TestIntake.test_idle_tick_makes_no_model_call)
v2 qualification: no LLM inference when there is no work. ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-mw1pg7se/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-mw1pg7se/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_malformed_metadata_does_not_crash_and_creates_no_work (test_stepc.TestIntake.test_malformed_metadata_does_not_crash_and_creates_no_work) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-4m8e21fp/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-4m8e21fp/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_own_post_is_never_ingested_ack_loop (test_stepc.TestIntake.test_own_post_is_never_ingested_ack_loop) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-0wkatnmr/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-0wkatnmr/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_receipt_ack_is_posted_after_persistence (test_stepc.TestIntake.test_receipt_ack_is_posted_after_persistence) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-hgoh167h/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-hgoh167h/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok
test_restart_replay_does_not_duplicate_the_inbox_write (test_stepc.TestIntake.test_restart_replay_does_not_duplicate_the_inbox_write) ... /tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/watch_c.py:104: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-hyhrf8q4/gt/.runtime/henry-watch.state' mode='r' encoding='utf-8'>
  raw = open(STATE).read().strip()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:157: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-hyhrf8q4/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  pass
ResourceWarning: Enable tracemalloc to get the object allocation traceback
/tmp/claude-1001/-home-marccressall-gt-mayor/63c6a2be-6a07-4242-a2e2-fb0655f36d45/scratchpad/stepc/test_stepc.py:85: ResourceWarning: unclosed file <_io.TextIOWrapper name='/tmp/stepc-home-hyhrf8q4/gt/.runtime/henry-watch.lock' mode='w' encoding='utf-8'>
  return self.m.main()
ResourceWarning: Enable tracemalloc to get the object allocation traceback
ok

----------------------------------------------------------------------
Ran 10 tests in 0.017s

OK
```

## 3. Sabotage controls (each guard removed in turn, suite must FAIL)

```
$ remove the ledger fence                        -> FAILED (failures=1)
$ remove the receipt-ACK call                    -> FAILED (failures=2)
$ mark acked before post_ack returns             -> FAILED (failures=1)
$ remove the corrupt-state guard                 -> FAILED (errors=1)
$ call the local model on every tick             -> FAILED (failures=1)
```

## 4. Installed vs candidate (unified diff)

```diff
--- ~/gt/bin/gt-henry-watch	2026-09-21 11:13:57.373781666 -0600
+++ watch_c.py	2026-09-22 22:54:37.406575999 -0600
@@ -27,6 +27,9 @@
 HOME = os.path.expanduser("~")
 RUNTIME = os.path.join(HOME, "gt", ".runtime")
 STATE = os.path.join(RUNTIME, "henry-watch.state")
+LEDGER = os.path.join(RUNTIME, "henry-watch.ledger.json")   # per-message dedupe fence
+LEDGER_MAX = 5000            # stop visibly rather than silently evicting pending work
+LEDGER_RETAIN_DAYS = 14      # replay fence horizon
 LOCK = os.path.join(RUNTIME, "henry-watch.lock")
 ENVFILE = os.path.join(HOME, ".config", "liveop", "e2e-staging.env")
 CHANNEL = "#dev"
@@ -112,6 +115,66 @@
     os.replace(tmp, STATE)
 
 
+def read_ledger():
+    if not os.path.exists(LEDGER):
+        return {}
+    with open(LEDGER) as f:
+        return json.load(f)          # a corrupt ledger raises: caller fails loudly
+
+
+def write_ledger(led):
+    tmp = LEDGER + ".tmp"
+    with open(tmp, "w") as f:
+        json.dump(led, f)
+        f.flush()
+        os.fsync(f.fileno())
+    os.replace(tmp, LEDGER)
+
+
+def mark(led, mid, stage, ts=""):
+    """Persist the dedupe key BEFORE the side effect it fences (v2 5C)."""
+    e = led.setdefault(mid, {})
+    e["stage"] = stage
+    e["at"] = int(datetime.now(timezone.utc).timestamp())
+    if ts:
+        e["ts"] = ts
+    write_ledger(led)
+
+
+def prune_ledger(led):
+    cutoff = int(datetime.now(timezone.utc).timestamp()) - LEDGER_RETAIN_DAYS * 86400
+    keep = {k: v for k, v in led.items()
+            if v.get("at", 0) >= cutoff or v.get("stage") != "acked"}
+    return keep
+
+
+def already_in_inbox(mid):
+    """Reconcile an ambiguous 'intent': did the inbox write actually land?
+    Searches every mail bead, including archived ones, for the message id."""
+    out = run(["bd", "-C", os.path.join(HOME, "gt"), "list", "--include-infra",
+               "--status=all", "--limit=0", "--json"])
+    return mid in (out or "")
+
+
+def post_ack(env, m):
+    """Durable-receipt ACK. Receipt only: never acceptance, never completion."""
+    md = meta(m)
+    body = ("Durable receipt ACK from the Gastown adapter for message " + m["id"] +
+            " (" + m["timestamp"] + "). The message is persisted in the Mayor's inbox.\n"
+            "This is RECEIPT ONLY: not acceptance, not a verdict, and not completion. "
+            "The Mayor answers separately.")
+    p = {"channel": CHANNEL, "sender": "mayor", "content": body, "replyTo": m["id"],
+         "metadata": {"kind": "ACK", "ackKind": "receipt", "recipient":
+                      md.get("authenticatedAgentId") or "agent:henry",
+                      "correlationId": md.get("correlationId"), "acks": [m["id"]]}}
+    body_bytes = json.dumps({"operation": "agentPostMessage", "params": p}).encode()
+    req = urllib.request.Request(env["LIVEOP_API_URL"], data=body_bytes, method="POST",
+                                 headers={"content-type": "application/json",
+                                          "x-api-key": env["LIVEOP_API_KEY"]})
+    with urllib.request.urlopen(req, timeout=45) as r:
+        json.load(r)
+
+
 def summarize(text):
     """One-line summary from the local model. Returns None on any failure;
     delivery never depends on it."""
@@ -176,17 +239,26 @@
         log("SKIP previous run still holds the lock")
         return 0
 
-    last, seen = read_state()
-    msgs = poll(load_env())
+    try:
+        last, seen = read_state()
+        led = prune_ledger(read_ledger())
+    except Exception as e:  # noqa: BLE001 - corrupt durable state must stop the tick
+        log(f"ERROR CORRUPT durable state, refusing to run: {e!r}")
+        return 2
+    if len(led) > LEDGER_MAX:
+        log(f"ERROR ledger at capacity ({len(led)}); stopping rather than evicting fences")
+        return 3
+    env = load_env()
+    msgs = poll(env)
     oldest = min((m["timestamp"] for m in msgs), default="")
 
     henry = sorted((m for m in msgs if wanted(m) and not m.get("isDeleted")),
                    key=lambda m: (m["timestamp"], m["id"]))
     if not last:
         # First run: mark what exists as seen rather than replaying history.
-        mark = henry[-1]["timestamp"] if henry else now()
-        write_state(mark, [m["id"] for m in henry if m["timestamp"] == mark])
-        log(f"INIT window={len(msgs)} henry={len(henry)} last_seen set to {mark}")
+        init_ts = henry[-1]["timestamp"] if henry else now()
+        write_state(init_ts, [m["id"] for m in henry if m["timestamp"] == init_ts])
+        log(f"INIT window={len(msgs)} henry={len(henry)} last_seen set to {init_ts}")
         return 0
 
     # >= plus the id set at the mark: two messages with an identical timestamp
@@ -196,22 +268,51 @@
     overflow = bool(oldest) and len(msgs) >= 100 and oldest > last
     delivered = 0
     for m in new:
-        subject = deliver(m, overflow)
-        # Advance the mark per message, only after the mail is persisted.
-        seen = (seen if m["timestamp"] == last else []) + [m["id"]]
+        mid = m["id"]
+        e = led.get(mid, {})
+        if e.get("stage") in ("delivered", "acked"):
+            continue                      # idempotent: this id is already fenced
+        if e.get("stage") == "intent":
+            # Ambiguous: we may have crashed mid-send. Reconcile before retrying,
+            # rather than risking either a duplicate or a silent loss.
+            if already_in_inbox(mid):
+                log(f"RECONCILED {mid} already in the inbox; not re-delivering")
+                mark(led, mid, "delivered", m["timestamp"])
+                e = led[mid]
+            else:
+                log(f"RECONCILED {mid} absent from the inbox; re-delivering")
+        if led.get(mid, {}).get("stage") != "delivered":
+            mark(led, mid, "intent", m["timestamp"])   # fence BEFORE the side effect
+            subject = deliver(m, overflow)
+            mark(led, mid, "delivered", m["timestamp"])
+            log(f"DELIVERED {mid} {m['timestamp']} :: {subject}")
+        # The watermark advances only after the inbox write is durable.
+        seen = (seen if m["timestamp"] == last else []) + [mid]
         last = m["timestamp"]
         write_state(last, seen)
         delivered += 1
-        log(f"DELIVERED {m['id']} {m['timestamp']} :: {subject}")
     if delivered:
         # Wake only; the nudge carries no content (gt-4iuw: keep it dash-free).
         try:
             run(["gt", "nudge", "mayor", f"Henry posted {delivered} new message(s) in live-op dev. Check your inbox."])
         except Exception as e:  # noqa: BLE001 - mail is already persisted
             log(f"WARN nudge failed, mail is in the inbox: {e!r}")
+    # Receipt-ACK pass: every delivered-but-unacked id, including earlier ticks.
+    # A failed ACK is never recorded as sent; it simply retries next tick.
+    acked = 0
+    for m in henry:
+        if led.get(m["id"], {}).get("stage") == "delivered":
+            try:
+                post_ack(env, m)
+                mark(led, m["id"], "acked")
+                acked += 1
+            except Exception as e:  # noqa: BLE001 - stays 'delivered', retried
+                log(f"WARN ACK failed for {m['id']}, will retry: {e!r}")
+    if acked:
+        log(f"ACKED {acked}")
     if overflow and not new:
         log(f"WARN window oldest {oldest} is newer than last_seen {last}; messages may be missed")
-    log(f"OK window={len(msgs)} oldest={oldest} henry={len(henry)} new={delivered} last_seen={last}")
+    log(f"OK window={len(msgs)} ledger={len(led)} oldest={oldest} henry={len(henry)} new={delivered} last_seen={last}")
     return 0
 
 
```
