"""Step C RED suite for the Henry->Mayor intake adapter (v2 section 5C).

Every test drives the module's own main() with the network, the mail command and
the local model replaced. Nothing here touches the live state, the live log, the
live channel or the installed script: HOME is redirected to a temp dir.

Cases required by v2 5C: restart replay, duplicate request, failed ACK, corrupt
state, disk-write failure, foreign recipient, malformed metadata, ACK loop.
"""
import importlib.machinery
import importlib.util
import json
import os
import shutil
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
TARGET = os.environ.get("STEPC_TARGET", os.path.join(HERE, "watch_c.py"))


def load(home):
    """Load the adapter with HOME redirected, so RUNTIME/STATE/LOCK are temp."""
    os.environ["HOME"] = home
    ld = importlib.machinery.SourceFileLoader("watch_c_" + os.path.basename(home), TARGET)
    spec = importlib.util.spec_from_loader(ld.name, ld)
    m = importlib.util.module_from_spec(spec)
    ld.exec_module(m)
    m.summarize = lambda text: "SUMMARY"
    m.load_env = lambda: {"LIVEOP_API_URL": "http://test.invalid", "LIVEOP_API_KEY": "k"}
    return m


def msg(mid, ts, auth="agent:henry", recipient=None, kind="review-handoff", content="body",
        meta=True, requires_ack=True):
    # requiresAck defaults to literal true: Henry ACKs only what asks for one.
    md = {"authenticatedAgentId": auth, "kind": kind, "requiresAck": requires_ack}
    if recipient:
        md["recipient"] = recipient
    return {"id": mid, "timestamp": ts, "sender": "henry", "content": content,
            "replyTo": None, "isDeleted": False, "metadata": md if meta else None}


class Harness(unittest.TestCase):
    def setUp(self):
        self.home = tempfile.mkdtemp(prefix="stepc-home-")
        os.makedirs(os.path.join(self.home, "gt", ".runtime"), exist_ok=True)
        self.real_home = os.environ.get("HOME")
        self.m = load(self.home)
        self.mail = []          # subjects handed to `gt mail send`
        self.posts = []         # what the adapter posted back to the channel
        self.window = []
        self.fail_mail = False
        self.fail_post = False
        self.fail_state_write = False

        def fake_run(cmd):
            if cmd[:3] == ["gt", "mail", "send"]:
                if self.fail_mail:
                    raise RuntimeError("mail send failed")
                self.mail.append(cmd[cmd.index("-s") + 1])   # the subject, not the flag
            return ""

        self.m.run = fake_run
        self.m.poll = lambda env: list(self.window)
        # post_ack may not exist yet (that is the point of the RED suite)
        if hasattr(self.m, "post_ack"):
            real = self.m.post_ack

            def fake_post(env, message):
                if self.fail_post:
                    raise RuntimeError("post failed")
                self.posts.append(message["id"])
            self.m.post_ack = fake_post
        if self.fail_state_write:
            pass

    def tearDown(self):
        if self.real_home:
            os.environ["HOME"] = self.real_home
        shutil.rmtree(self.home, ignore_errors=True)

    def seed(self, ts="2026-09-23T00:00:00.000Z", ids=()):
        self.m.write_state(ts, list(ids))

    def run_tick(self):
        return self.m.main()


class TestIntake(Harness):
    # ---- controls that must already pass -------------------------------
    def test_foreign_recipient_creates_no_work(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", auth="agent:ven", recipient="casey")]
        self.run_tick()
        self.assertEqual(self.mail, [], "a message addressed to another agent must not be delivered")

    def test_malformed_metadata_does_not_crash_and_creates_no_work(self):
        self.seed()
        self.window = [
            msg("m1", "2026-09-23T01:00:00.000Z", meta=False),
            {"id": "m2", "timestamp": "2026-09-23T01:00:01.000Z", "sender": "x",
             "content": "c", "metadata": "{not json"},
            {"id": "m3", "timestamp": "2026-09-23T01:00:02.000Z", "sender": "x",
             "content": "c", "metadata": ["list", "not", "object"]},
        ]
        self.run_tick()
        self.assertEqual(self.mail, [], "malformed metadata must be quarantined, not delivered")

    def test_own_post_is_never_ingested_ack_loop(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", auth="agent:gas-new",
                           recipient="gastown", kind="ACK")]
        self.run_tick()
        self.assertEqual(self.mail, [], "our own ACK must not create work (ACK loop)")

    def test_idle_tick_makes_no_model_call(self):
        """v2 qualification: no LLM inference when there is no work."""
        calls = []
        self.m.summarize = lambda t: calls.append(t) or "S"
        self.seed(ts="2026-09-23T09:00:00.000Z")
        self.window = [msg("old", "2026-09-23T08:00:00.000Z")]   # older than the mark
        self.run_tick()
        self.assertEqual(calls, [], "an idle tick must not call the local model")
        self.assertEqual(self.mail, [])

    # ---- the RED set ---------------------------------------------------
    def test_receipt_ack_is_posted_after_persistence(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        self.run_tick()
        self.assertEqual(len(self.mail), 1, "message should be persisted to the inbox")
        self.assertTrue(hasattr(self.m, "post_ack"), "adapter must have a receipt-ACK path")
        self.assertEqual(self.posts, ["m1"], "a durable receipt ACK must be posted for m1")

    def test_failed_ack_is_not_recorded_as_sent_and_retries(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        self.fail_post = True
        self.run_tick()
        self.fail_post = False
        self.posts.clear()
        self.run_tick()   # second natural tick
        self.assertEqual(self.posts, ["m1"], "a failed ACK must be retried on the next tick")
        self.assertEqual(len(self.mail), 1, "retrying the ACK must not re-deliver the message")

    def test_restart_replay_does_not_duplicate_the_inbox_write(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        self.fail_state_write = True
        orig = self.m.write_state

        def boom(ts, ids):
            raise OSError("disk full")
        self.m.write_state = boom
        try:
            self.run_tick()      # crashes after the mail is sent
        except Exception:
            pass
        self.m.write_state = orig
        n_after_crash = len(self.mail)
        self.run_tick()          # restart: same message still in the window
        self.assertEqual(len(self.mail), n_after_crash,
                         "a crash between the inbox write and the state write must not "
                         "produce a second inbox write for the same message id")

    def test_duplicate_delivery_of_same_id_is_idempotent(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        self.run_tick()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new"),
                       msg("m2", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]  # identical timestamp
        self.run_tick()
        self.assertEqual(len(self.mail), 2, "m1 must not be delivered twice; m2 must be delivered once")

    def test_corrupt_state_is_not_read_as_first_run(self):
        open(self.m.STATE, "w").write("{ this is not json")
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        rc = self.run_tick()
        self.assertEqual(self.mail, [], "corrupt state must stop the tick, not replay history")
        self.assertNotEqual(rc, 0, "a corrupt state file must fail loudly")

    def test_disk_write_failure_does_not_ack(self):
        self.seed()
        self.window = [msg("m1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new")]
        self.m.write_state = lambda ts, ids: (_ for _ in ()).throw(OSError("read-only fs"))
        try:
            self.run_tick()
        except Exception:
            pass
        self.assertEqual(self.posts, [], "no ACK may be posted when durable state could not be written")


if __name__ == "__main__":
    unittest.main(verbosity=2)


class TestC2Allowlist(Harness):
    """C2 (Henry, 2650a920): explicit actionable allowlist, visible bounded
    quarantine, no dispatch from quarantine, correlated reason evidence without
    payloads, no ACK loops or foreign-recipient effects, restart-safe."""

    def test_only_allowlisted_kinds_become_actionable(self):
        self.seed()
        self.window = [
            msg("a1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new", kind="REQUEST"),
            msg("a2", "2026-09-23T01:00:01.000Z", recipient="agent:gas-new", kind="REVIEW_REQUEST"),
            msg("a3", "2026-09-23T01:00:02.000Z", recipient="agent:gas-new", kind="review-handoff"),
            msg("a4", "2026-09-23T01:00:03.000Z", recipient="agent:gas-new", kind="DECISION"),
        ]
        self.run_tick()
        self.assertEqual(len(self.mail), 4, "targeted allowlisted kinds must be delivered")
        self.assertEqual(sorted(self.posts), ["a1", "a2", "a3", "a4"], "each actionable message gets one receipt ACK")

    def test_status_and_ack_do_not_create_work_or_ack_loops(self):
        self.seed()
        self.window = [
            msg("s1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new", kind="STATUS"),
            msg("k1", "2026-09-23T01:00:01.000Z", recipient="agent:gas-new", kind="ACK"),
        ]
        self.run_tick()
        self.assertEqual(self.posts, [], "never ACK a STATUS or an ACK: that is the loop")
        for subj in self.mail:
            self.assertIn("FYI", subj, "non-actionable mail must be marked as such")

    def test_unknown_identity_is_quarantined_not_delivered(self):
        self.seed()
        self.window = [msg("u1", "2026-09-23T01:00:00.000Z", auth="agent:stranger",
                           recipient="agent:gas-new", kind="REQUEST")]
        self.run_tick()
        self.assertEqual(self.mail, [], "an unknown sender must not reach the actionable inbox")
        self.assertEqual(self.posts, [], "and must not be ACKed")
        q = self.m.read_quarantine()
        self.assertIn("u1", q, "it must leave a VISIBLE quarantine record")
        self.assertIn("identity", q["u1"]["reason"], "the record must carry a correlated reason")

    def test_quarantine_records_carry_no_payload(self):
        self.seed()
        secret = "SENSITIVE-PAYLOAD-DO-NOT-STORE"
        self.window = [msg("u2", "2026-09-23T01:00:00.000Z", auth="agent:stranger",
                           recipient="agent:gas-new", kind="REQUEST", content=secret)]
        self.run_tick()
        blob = json.dumps(self.m.read_quarantine())
        self.assertNotIn(secret, blob, "quarantine evidence must not store message payloads")

    def test_quarantine_is_never_dispatched_on_a_later_tick(self):
        self.seed()
        self.window = [msg("u3", "2026-09-23T01:00:00.000Z", auth="agent:stranger",
                           recipient="agent:gas-new", kind="REQUEST")]
        self.run_tick()
        self.run_tick()            # same message still in the window
        self.assertEqual(self.mail, [], "a quarantined message must never later be dispatched")

    def test_quarantine_survives_restart_and_is_bounded(self):
        self.seed()
        self.window = [msg(f"b{i}", "2026-09-23T01:00:00.000Z", auth="agent:stranger",
                           recipient="agent:gas-new", kind="REQUEST") for i in range(5)]
        self.run_tick()
        m2 = load(self.home)       # fresh process, same HOME: restart
        self.assertEqual(len(m2.read_quarantine()), 5, "quarantine must be durable across restart")
        self.assertTrue(hasattr(m2, "QUARANTINE_MAX"), "quarantine must be bounded")

    def test_foreign_recipient_has_no_effect_even_from_henry(self):
        self.seed()
        self.window = [msg("f1", "2026-09-23T01:00:00.000Z", recipient="agent:casey", kind="REQUEST")]
        self.run_tick()
        self.assertEqual(self.mail, [], "a message targeted at another agent is not ours to act on")
        self.assertEqual(self.posts, [], "and must not be ACKed")

    def test_unsupported_kind_from_a_trusted_sender_is_quarantined(self):
        """The allowlist is a positive list: a kind nobody agreed on does not
        become work just because Henry sent it."""
        self.seed()
        self.window = [msg("x1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new",
                           kind="PLEASE_DEPLOY_EVERYTHING")]
        self.run_tick()
        self.assertEqual(self.mail, [], "an unsupported kind must not be delivered")
        self.assertEqual(self.posts, [], "and must not be ACKed")
        q = self.m.read_quarantine()
        self.assertIn("x1", q, "it must leave a visible quarantine record")
        self.assertIn("unsupported kind", q["x1"]["reason"])


class TestC2Henry(Harness):
    """Henry's independent step C findings (a209a305), as rejection regressions."""

    def test_application_error_envelope_does_not_count_as_a_sent_ack(self):
        """(2) An HTTP 200 carrying an error envelope is NOT a sent ACK.

        Drives the REAL post_ack through a faked urlopen, so bypassing the
        validator is detectable."""
        import contextlib
        import io
        self.seed()
        self.window = [msg("e1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new", kind="REQUEST")]
        del self.m.post_ack      # restore the module's own implementation
        import importlib.machinery, importlib.util
        ld = importlib.machinery.SourceFileLoader("reload_e1", TARGET)
        spec = importlib.util.spec_from_loader(ld.name, ld)
        fresh = importlib.util.module_from_spec(spec)
        ld.exec_module(fresh)
        fresh.summarize = lambda t: "S"
        fresh.load_env = self.m.load_env
        fresh.run = self.m.run
        fresh.poll = self.m.poll

        @contextlib.contextmanager
        def fake_urlopen(req, timeout=0):
            yield io.StringIO(json.dumps({"errors": [{"message": "AccessDenied"}]}))
        fresh.urllib.request.urlopen = fake_urlopen
        fresh.main()
        led = fresh.read_ledger()
        self.assertEqual(led["e1"]["stage"], "delivered",
                         "an error envelope must leave the ACK unsent, not 'acked'")

    def test_pending_ack_survives_the_message_leaving_the_poll_window(self):
        """(3) The retry must not depend on the source message still being visible."""
        self.seed()
        self.window = [msg("w1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new", kind="REQUEST")]
        self.fail_post = True
        self.run_tick()                     # delivered, ACK failed
        self.fail_post = False
        self.posts.clear()
        self.window = []                    # the message has scrolled out of the window
        self.run_tick()
        self.assertEqual(self.posts, ["w1"], "a pending ACK must be retried from durable state")

    def test_reconcile_requires_an_exact_id_match(self):
        """(4) A substring hit on unrelated mail must not count as reconciled."""
        self.seed()
        self.m.run = lambda cmd: ('[{"description":"message id: w1-UNRELATED-SUFFIX"}]'
                                  if cmd[0] == "bd" else "")
        self.assertFalse(self.m.already_in_inbox("w1"),
                         "a longer id containing ours is not ours")
        self.m.run = lambda cmd: ('[{"description":"message id: w1"}]' if cmd[0] == "bd" else "")
        self.assertTrue(self.m.already_in_inbox("w1"), "an exact id must reconcile")

    def test_saturated_window_records_a_visible_gap_and_never_claims_drained(self):
        """(5) A full window whose oldest row is newer than the mark may hide rows."""
        self.seed(ts="2026-09-20T00:00:00.000Z")
        self.window = [msg(f"g{i}", f"2026-09-23T02:{i:02d}:00.000Z",
                           recipient="agent:gas-new", kind="STATUS") for i in range(100)]
        self.run_tick()
        gaps = self.m.read_gaps()
        self.assertTrue(gaps, "a saturated window past the watermark must record a visible gap")
        self.assertIn("2026-09-20T00:00:00.000Z", json.dumps(gaps),
                      "the gap record must carry the watermark it could not reach")


class TestC2R3(Harness):
    """Henry's C2R2 adjudication (ekqqzhw): four remaining repairs."""

    def test_poll_envelope_with_errors_is_not_processed(self):
        """(1) HTTP 200 carrying errors AND data must not dispatch anything.

        Drives the REAL poll() through a faked urlopen, so bypassing the
        validator is detectable."""
        import contextlib, importlib.machinery, importlib.util, io
        self.seed()
        ld = importlib.machinery.SourceFileLoader("reload_p1", TARGET)
        spec = importlib.util.spec_from_loader(ld.name, ld)
        fresh = importlib.util.module_from_spec(spec)
        ld.exec_module(fresh)
        fresh.summarize = lambda t: "S"
        fresh.load_env = self.m.load_env
        fresh.run = self.m.run
        payload = {"errors": [{"message": "PartialFailure"}],
                   "data": {"messages": [msg("p1", "2026-09-23T01:00:00.000Z",
                                             recipient="agent:gas-new", kind="REQUEST")]}}

        @contextlib.contextmanager
        def fake_urlopen(req, timeout=0):
            yield io.StringIO(json.dumps(payload))
        fresh.urllib.request.urlopen = fake_urlopen
        rc = fresh.main()
        self.assertEqual(self.mail, [], "a poll envelope carrying errors must dispatch nothing")
        self.assertNotEqual(rc, 0, "and the tick must fail loudly")

    def test_ack_response_must_carry_the_posted_identity(self):
        """(1b) A 200 with data={} is not proof the ACK was stored."""
        with self.assertRaises(Exception):
            self.m.check_post_envelope({"data": {}})
        self.m.check_post_envelope({"data": {"id": "abc", "timestamp": "t"}})

    def test_missing_recipient_is_not_actionable(self):
        """(2) No recipient means not addressed to us: no ACK, no work."""
        self.seed()
        m = msg("n1", "2026-09-23T01:00:00.000Z", kind="REQUEST")   # no recipient
        self.window = [m]
        self.run_tick()
        self.assertEqual(self.posts, [], "an unaddressed message must not be ACKed")
        self.assertNotEqual(self.m.classify(m)[0], "actionable")

    def test_receipt_ack_requires_literal_requires_ack_true(self):
        """(2b) ACK only what explicitly asks for one."""
        self.seed()
        m = dict(msg("r1", "2026-09-23T01:00:00.000Z", recipient="agent:gas-new", kind="REQUEST"))
        m["metadata"] = dict(m["metadata"]); m["metadata"]["requiresAck"] = False
        self.window = [m]
        self.run_tick()
        self.assertEqual(len(self.mail), 1, "it is still delivered")
        self.assertEqual(self.posts, [], "but requiresAck=false must not be ACKed")

    def test_saturated_window_blocks_delivery_and_holds_the_cursor(self):
        """(3) A gapped window must not deliver or advance past the gap."""
        self.seed(ts="2026-09-20T00:00:00.000Z")
        self.window = [msg(f"s{i}", f"2026-09-23T02:00:{i:02d}.000Z",
                           recipient="agent:gas-new", kind="REQUEST") for i in range(60)] + \
                      [msg(f"t{i}", f"2026-09-23T02:01:{i:02d}.000Z",
                           recipient="agent:gas-new", kind="REQUEST") for i in range(40)]
        before_ts, _ = self.m.read_state()
        self.run_tick()
        after_ts, _ = self.m.read_state()
        self.assertEqual(self.mail, [], "a gapped window must not dispatch")
        self.assertEqual(self.posts, [], "and must not ACK")
        self.assertEqual(after_ts, before_ts, "the cursor must be held until reconciliation")
        self.assertTrue(self.m.read_gaps(), "and the gap must be recorded")

    def test_quarantine_write_failure_fails_closed(self):
        """(4) No durable quarantine record means no 'handled' state and no advance."""
        self.seed()
        self.m.QUARANTINE_MAX = 0          # capacity exhausted
        m = msg("q1", "2026-09-23T01:00:00.000Z", auth="agent:stranger",
                recipient="agent:gas-new", kind="REQUEST")
        self.window = [m]
        before_ts, _ = self.m.read_state()
        self.run_tick()
        after_ts, _ = self.m.read_state()
        self.assertEqual(self.m.read_ledger().get("q1", {}).get("stage"), None,
                         "without a durable record it must not be marked quarantined")
        self.assertEqual(after_ts, before_ts, "and the cursor must not advance")
