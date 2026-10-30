"""Control-flow tests use temporary Git repositories and a fake Codex, no tokens."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))
import runner


BOARD = """## К выполнению
- [ ] [[docs/tasks#S1-API — API|S1-API — API]]
- [ ] [[docs/tasks#S1-UI — UI|S1-UI — UI]]
- [ ] [[docs/tasks#S1-DEMO — Demo|S1-DEMO — Demo]]
## В работе
## На проверке
## Готово
- [x] [[docs/tasks#S1-CORE — Core|S1-CORE — Core]]
"""
TASKS = """## S1-CORE — Core
- **После:** B2.
## S1-API — API
- **После:** S1-CORE.
## S1-UI — UI
- **После:** S1-API.
## S1-DEMO — Demo
- **После:** S1-UI.
"""


class RunnerTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repo"
        self.repo.mkdir()
        runner.git(self.repo, "init", "-b", "prepare/in-memory")
        runner.git(self.repo, "config", "user.name", "Test")
        runner.git(self.repo, "config", "user.email", "test@example.invalid")
        (self.repo / "docs").mkdir()
        for name, text in {"Kanban.md": BOARD, "docs/tasks.md": TASKS,
                           "AGENTS.md": "test instructions\n", "docs/delivery.md": "journal\n",
                           "docs/stages.md": "stage contract\n"}.items():
            (self.repo / name).write_text(text)
        runner.git(self.repo, "add", ".")
        runner.git(self.repo, "commit", "-m", "initial")
        runner.git(self.repo, "branch", "stage/1-election")
        self.args = argparse.Namespace(repo=str(self.repo), journal_branch="prepare/in-memory",
                                       max_tasks=1, sandbox="workspace-write", repair=False,
                                       task_timeout=5, check_timeout=5)
        self.r = runner.Runner(self.args)
        self.original = runner.git(self.repo, "rev-parse", "stage/1-election")

    def prepare_result(self):
        self.r.start(self.r.select())
        worker = Path(self.r.state["worker"])
        (worker / "Kanban.md").write_text(runner.move_card(BOARD, "S1-API", "Готово"))
        (worker / "feature.txt").write_text("implemented\n")
        runner.git(worker, "add", ".")
        runner.git(worker, "commit", "-m", "implement API")
        sha = runner.git(worker, "rev-parse", "HEAD")
        report = {"task_id": "S1-API", "status": "done", "reason": "implemented",
                  "commit": sha, "extra_checks": "API contract tests PASS", "limitations": ""}
        output = Path(self.r.state["directory"]) / "result.json"
        output.write_text(json.dumps(report))
        self.r.state.update(result=str(output), phase="verify")
        self.r.save()
        return sha

    @staticmethod
    def pass_check(command, cwd, logfile, timeout):
        logfile.write_text("")
        return 0

    def test_selection_and_board_round_trip(self):
        self.assertEqual(self.r.select()[0], "S1-API")
        moved = runner.move_card(BOARD, "S1-API", "Готово")
        self.assertEqual(runner.board(moved)["S1-API"], "Готово")
        with self.assertRaises(runner.Stop):
            runner.board(BOARD + "- [x] [[docs/tasks#S1-API — API|API]]\n")

    def test_pending_dependency_and_demo_stop(self):
        boardfile = self.repo / "Kanban.md"
        boardfile.write_text(runner.move_card(runner.move_card(BOARD, "S1-API", "Готово"), "S1-UI", "Готово"))
        runner.git(self.repo, "add", ".")
        runner.git(self.repo, "commit", "-m", "done")
        with self.assertRaisesRegex(runner.Stop, "приёмка"):
            self.r.select()

    def test_active_card_stops(self):
        (self.repo / "Kanban.md").write_text(runner.move_card(BOARD, "S1-API", "В работе"))
        runner.git(self.repo, "add", ".")
        runner.git(self.repo, "commit", "-m", "active")
        with self.assertRaisesRegex(runner.Stop, "незавершённые"):
            self.r.select()

    def test_done_report_does_not_override_failed_race(self):
        self.prepare_result()
        def checks(command, cwd, logfile, timeout):
            logfile.write_text("race detected" if "-race" in command else "")
            return 1 if "-race" in command else 0
        with patch.object(runner, "execute", checks), self.assertRaises(runner.Stop):
            self.r.run(True)
        self.assertEqual(runner.git(self.repo, "rev-parse", "stage/1-election"), self.original)
        self.assertEqual(runner.board((self.repo / "Kanban.md").read_text())["S1-API"], "К выполнению")

    def test_integration_and_journal_are_idempotent(self):
        sha = self.prepare_result()
        with patch.object(runner, "execute", self.pass_check):
            self.r.run(True)
        self.assertEqual(runner.git(self.repo, "rev-parse", "stage/1-election"), sha)
        self.assertEqual(runner.board((self.repo / "Kanban.md").read_text())["S1-API"], "Готово")
        self.r.finish()
        text = (self.repo / "docs/delivery.md").read_text()
        self.assertEqual(text.count(f"autokanban:S1-API:{sha}"), 1)
        self.assertEqual(self.r.select()[0], "S1-UI")

    def test_moved_target_is_not_overwritten(self):
        self.prepare_result()
        runner.git(self.repo, "update-ref", "refs/heads/stage/1-election",
                   runner.git(self.r.state["worker"], "rev-parse", "HEAD~1"))
        with patch.object(runner, "execute", self.pass_check), self.assertRaisesRegex(runner.Stop, "изменилась"):
            self.r.run(True)

    def test_dirty_worker_is_rejected(self):
        self.prepare_result()
        (Path(self.r.state["worker"]) / "foreign.txt").write_text("uncommitted")
        with self.assertRaisesRegex(runner.Stop, "незакоммиченные"):
            self.r.verify()

    def test_formatting_output_is_failure_even_with_exit_zero(self):
        self.prepare_result()
        def checks(command, cwd, logfile, timeout):
            logfile.write_text("bad.go\n" if command[0] == "gofmt" else "")
            return 0
        with patch.object(runner, "execute", checks), self.assertRaises(runner.Stop):
            self.r.verify()

    def test_fake_codex_blocked_and_resume_same_session(self):
        self.r.start(self.r.select())
        commands = []
        def codex(command, cwd, logfile, timeout):
            commands.append(command)
            output = Path(command[command.index("-o") + 1])
            output.write_text(json.dumps({"task_id": "S1-API", "status": "blocked", "reason": "no Docker",
                                          "commit": "", "extra_checks": "", "limitations": ""}))
            logfile.write_text(json.dumps({"type": "thread.started", "thread_id": "session-test"}) + "\n" +
                               json.dumps({"type": "turn.completed", "usage": {"input_tokens": 100, "cached_input_tokens": 50}}))
            return 0
        with patch.object(runner, "execute", codex):
            for _ in range(2):
                with self.assertRaisesRegex(runner.Stop, "blocked: no Docker"):
                    self.r.agent()
        self.assertIn("resume", commands[1])
        self.assertIn("session-test", commands[1])
        self.assertEqual(len(self.r.state["usage"]), 2)
        self.assertEqual(runner.git(self.repo, "rev-parse", "stage/1-election"), self.original)

    def test_missing_json_stops_even_after_completed_turn(self):
        self.r.start(self.r.select())
        def codex(command, cwd, logfile, timeout):
            logfile.write_text('{"type":"turn.completed"}\n')
            return 0
        with patch.object(runner, "execute", codex), self.assertRaisesRegex(runner.Stop, "итогового JSON"):
            self.r.agent()

    def test_timeout_terminates_child(self):
        logfile = Path(self.temp.name) / "timeout.log"
        with self.assertRaisesRegex(runner.Stop, "таймаут"):
            runner.execute([sys.executable, "-c", "import time; time.sleep(30)"], self.repo, logfile, 0.05)

    def test_usage_and_error_events(self):
        logfile = Path(self.temp.name) / "events.jsonl"
        logfile.write_text('noise\n{"type":"turn.failed"}\n{"type":"turn.completed","usage":{"input_tokens":5}}\n')
        _, usage, healthy = runner.events(logfile)
        self.assertFalse(healthy)
        self.assertEqual(usage["input_tokens"], 5)

    def test_cli_saves_blocked_state_without_starting_next_task(self):
        bindir = Path(self.temp.name) / "bin"
        bindir.mkdir()
        fake = bindir / "codex"
        fake.write_text(f"#!{sys.executable}\n" + '''import json, pathlib, sys
output = pathlib.Path(sys.argv[sys.argv.index('-o') + 1])
output.write_text(json.dumps(dict(task_id='S1-API', status='blocked', reason='missing browser',
                                 commit='', extra_checks='', limitations='')))
print(json.dumps(dict(type='thread.started', thread_id='fake-cli-session')))
print(json.dumps(dict(type='turn.completed', usage=dict(input_tokens=12))))
''')
        fake.chmod(0o755)
        result = subprocess.run([sys.executable, str(Path(runner.__file__)), "run", "--repo", str(self.repo),
                                 "--max-tasks", "2"], text=True, capture_output=True,
                                env={**os.environ, "PATH": str(bindir) + os.pathsep + os.environ["PATH"]})
        self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
        self.assertIn("blocked: missing browser", result.stderr)
        saved = json.loads(self.r.statefile.read_text())
        self.assertEqual(saved["task"], "S1-API")
        self.assertEqual(saved["session"], "fake-cli-session")
        self.assertIn("missing browser", saved["error"])
        self.assertEqual(runner.git(self.repo, "rev-parse", "stage/1-election"), self.original)

    def test_cli_repository_lock_prevents_second_runner(self):
        import fcntl
        with (self.r.root / "lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = subprocess.run([sys.executable, str(Path(runner.__file__)), "run", "--repo", str(self.repo)],
                                    text=True, capture_output=True)
        self.assertEqual(result.returncode, 1)
        self.assertIn("уже запущен", result.stderr)

    def test_report_rejects_limitations_wrong_id_and_false_success(self):
        report = {"task_id": "S1-API", "status": "done", "reason": "OK", "commit": "a" * 40,
                  "extra_checks": "contract PASS", "limitations": ""}
        for change in [{"task_id": "S1-UI"}, {"limitations": "race not run"},
                       {"status": "failed"}, {"commit": "HEAD"}, {"extra_checks": ""}]:
            with self.subTest(change=change), self.assertRaises(runner.Stop):
                runner.validate_result({**report, **change}, "S1-API")

    def test_cli_two_tasks_then_demo_boundary(self):
        bindir = Path(self.temp.name) / "bin"
        bindir.mkdir()
        fake = bindir / "codex"
        fake.write_text(f"#!{sys.executable}\nimport sys\nsys.path.insert(0, {str(Path(runner.__file__).parent)!r})\n" + '''import json, pathlib, re, runner
task = re.search(r'поручил только (S[1-4]-[A-Z]+)', sys.argv[-1])[1]
board = pathlib.Path('Kanban.md')
board.write_text(runner.move_card(board.read_text(), task, 'Готово'))
pathlib.Path(task + '.txt').write_text('implemented')
runner.git('.', 'add', '.')
runner.git('.', 'commit', '-m', 'implement ' + task)
sha = runner.git('.', 'rev-parse', 'HEAD')
pathlib.Path(sys.argv[sys.argv.index('-o') + 1]).write_text(json.dumps(dict(
    task_id=task, status='done', reason='OK', commit=sha, extra_checks='contract PASS', limitations='')))
print(json.dumps(dict(type='thread.started', thread_id=task)))
print(json.dumps(dict(type='turn.completed', usage=dict(input_tokens=10))))
''')
        fake.chmod(0o755)
        for name in ["go", "gofmt"]:
            binary = bindir / name
            binary.write_text(f"#!{sys.executable}\n")
            binary.chmod(0o755)
        command = [sys.executable, str(Path(runner.__file__)), "run", "--repo", str(self.repo), "--max-tasks", "2"]
        result = subprocess.run(command, text=True, capture_output=True,
                                env={**os.environ, "PATH": str(bindir) + os.pathsep + os.environ["PATH"]})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        statuses = runner.board((self.repo / "Kanban.md").read_text())
        self.assertEqual(statuses["S1-API"], "Готово")
        self.assertEqual(statuses["S1-UI"], "Готово")
        self.assertEqual(statuses["S1-DEMO"], "К выполнению")
        self.assertEqual(len(list((self.r.root / "runs").iterdir())), 2)
        with self.assertRaisesRegex(runner.Stop, "приёмка"):
            self.r.select()

    def test_status_available_while_runner_locked(self):
        import fcntl
        with (self.r.root / "lock").open("w") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            result = subprocess.run([sys.executable, str(Path(runner.__file__)), "status", "--repo", str(self.repo)],
                                    text=True, capture_output=True)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(json.loads(result.stdout)["phase"], "idle")


if __name__ == "__main__":
    unittest.main()
