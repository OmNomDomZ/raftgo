"""Sequential raftgo task runner. Never interprets model text as shell code."""
import argparse
import fcntl
import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import time
import uuid


class Stop(RuntimeError):
    pass


STAGES = ["prepare/in-memory", "stage/1-election", "stage/2-log",
          "stage/3-kv", "stage/4-cas-lock"]
FIELDS = {"task_id": {"type": "string"},
          "status": {"type": "string", "enum": ["done", "blocked", "failed", "needs_input"]},
          "reason": {"type": "string"}, "commit": {"type": "string"},
          "extra_checks": {"type": "string"}, "limitations": {"type": "string"}}
SCHEMA = {"type": "object", "properties": FIELDS,
          "required": list(FIELDS), "additionalProperties": False}
CARD = re.compile(r"^- \[([ x])\] .*?docs/tasks#([A-Z0-9]+(?:-[A-Z]+)?)\s", re.M)


def git(repo, *args, check=True):
    result = subprocess.run(["git", "-C", str(repo), *args], text=True,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if check and result.returncode:
        raise Stop(result.stderr.strip() or f"git {args}: exit {result.returncode}")
    return result.stdout.strip()


def clean(repo):
    if git(repo, "status", "--porcelain"):
        raise Stop(f"Есть незакоммиченные изменения: {repo}")


def read_ref(repo, ref, name):
    return git(repo, "show", f"{ref}:{name}")


def board(text):
    result = {}
    section = None
    for line in text.splitlines():
        if line.startswith("## "):
            section = line[3:]
        match = CARD.match(line)
        if match:
            checked, task = match.groups()
            if task in result:
                raise Stop(f"Повтор карточки {task}")
            if (checked == "x") != (section == "Готово"):
                raise Stop(f"Несогласованный статус {task}")
            if section not in {"К выполнению", "В работе", "На проверке", "Готово"}:
                raise Stop(f"Неизвестная колонка {section}")
            result[task] = section
    if not result:
        raise Stop("В канбане не найдены карточки")
    return result


def move_card(text, task, section):
    lines = text.splitlines()
    matches = [i for i, line in enumerate(lines)
               if (m := CARD.match(line)) and m[2] == task]
    if len(matches) != 1:
        raise Stop(f"Карточка {task} отсутствует или повторяется")
    line = lines.pop(matches[0])
    line = re.sub(r"^- \[[ x]\]", "- [x]" if section == "Готово" else "- [ ]", line)
    try:
        pos = lines.index("## " + section) + 1
    except ValueError:
        raise Stop(f"Нет колонки {section}") from None
    lines[pos:pos] = ["", line]
    return "\n".join(lines) + "\n"


def cards(text):
    result = {}
    for match in re.finditer(r"^## ([A-Z0-9]+(?:-[A-Z]+)?) — .*\n([\s\S]*?)(?=^## |\Z)", text, re.M):
        task, body = match.groups()
        after = re.search(r"\*\*После:\*\* ([^\n]+)", body)
        deps = re.findall(r"\b(?:B\d+|S\d+-[A-Z]+)\b", after[1]) if after else []
        result[task] = {"body": match[0], "deps": deps}
    return result


def layout(task):
    match = re.fullmatch(r"S([1-4])-(CORE|API|UI|CLIENT|DEMO)", task)
    if not match:
        raise Stop(f"Первая версия поддерживает только S1–S4: {task}")
    stage = int(match[1])
    target = STAGES[stage]
    return (STAGES[stage - 1] if match[2] == "CORE" else target), target


def worktrees(repo):
    result = {}
    path = None
    for line in git(repo, "worktree", "list", "--porcelain").splitlines():
        if line.startswith("worktree "):
            path = Path(line[9:])
        elif line.startswith("branch refs/heads/"):
            result[line[18:]] = path
    return result


def atomic_json(path, data):
    temp = path.with_suffix(".tmp")
    temp.write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n")
    temp.replace(path)


def execute(command, cwd, logfile, timeout):
    """One process group, bounded wall time; no model polling while it runs."""
    with logfile.open("wb") as output:
        proc = subprocess.Popen(command, cwd=cwd, stdout=output, stderr=subprocess.STDOUT,
                                start_new_session=True)
        try:
            return proc.wait(timeout=timeout)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            os.killpg(proc.pid, signal.SIGTERM)
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                os.killpg(proc.pid, signal.SIGKILL)
                proc.wait()
            raise Stop(f"Прервано или превышен таймаут; лог: {logfile}") from None


def events(path):
    session = None
    usage = {}
    failed = False
    completed = False
    for line in path.read_text(errors="replace").splitlines():
        try:
            item = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(item, dict):
            continue
        if item.get("type") == "thread.started":
            session = item.get("thread_id")
        if item.get("type") == "turn.completed":
            completed = True
            for key, value in item.get("usage", {}).items():
                if isinstance(value, int):
                    usage[key] = usage.get(key, 0) + value
        if item.get("type") in {"turn.failed", "error"}:
            failed = True
    return session, usage, completed and not failed


def validate_result(result, task):
    if not isinstance(result, dict) or set(result) != set(FIELDS):
        raise Stop("Неверная структура итогового JSON")
    if any(not isinstance(value, str) for value in result.values()):
        raise Stop("Поля итогового JSON должны быть строками")
    if result["task_id"] != task:
        raise Stop("Агент вернул другой ID")
    if result["status"] != "done":
        raise Stop(f"{result['status']}: {result['reason']}")
    if result["limitations"].strip():
        raise Stop(f"Необходим разбор ограничения: {result['limitations']}")
    if not re.fullmatch(r"[0-9a-f]{40}", result["commit"]):
        raise Stop("Нет полного SHA проверяемого результата")
    if not result["extra_checks"].strip():
        raise Stop("Нет отчёта о дополнительных проверках карточки")


class Runner:
    def __init__(self, args):
        self.args = args
        self.repo = Path(git(args.repo, "rev-parse", "--show-toplevel"))
        common = Path(git(self.repo, "rev-parse", "--path-format=absolute", "--git-common-dir"))
        self.root = common / "autokanban"
        self.root.mkdir(exist_ok=True)
        self.statefile = self.root / "state.json"
        self.state = json.loads(self.statefile.read_text()) if self.statefile.exists() else None

    def save(self):
        atomic_json(self.statefile, self.state)

    def journal(self):
        path = worktrees(self.repo).get(self.args.journal_branch)
        if path is None:
            raise Stop(f"Нужен checkout общего журнала {self.args.journal_branch}")
        clean(path)
        return path

    def select(self):
        text = read_ref(self.repo, self.args.journal_branch, "Kanban.md")
        statuses = board(text)
        catalog = cards(read_ref(self.repo, self.args.journal_branch, "docs/tasks.md"))
        if set(statuses) != set(catalog):
            raise Stop("Список ID в канбане не совпадает с docs/tasks.md")
        active = [task for task, status in statuses.items() if status in {"В работе", "На проверке"}]
        if active:
            raise Stop("Есть незавершённые карточки: " + ", ".join(active))
        for task, status in statuses.items():
            if status == "Готово":
                continue
            if task not in catalog or not catalog[task]["deps"]:
                raise Stop(f"Не удалось определить зависимости {task}")
            missing = [dep for dep in catalog[task]["deps"] if statuses.get(dep) != "Готово"]
            if missing:
                continue
            base, target = layout(task)
            if task.endswith("-DEMO"):
                raise Stop(f"Достигнута {task}: приёмка этапа запускается вручную")
            sha = git(self.repo, "rev-parse", "--verify", "refs/heads/" + base)
            base_board = board(read_ref(self.repo, base, "Kanban.md"))
            missing = [dep for dep in catalog[task]["deps"] if base_board.get(dep) != "Готово"]
            if missing:
                raise Stop(f"В базе {base} не готовы зависимости: {', '.join(missing)}")
            return task, catalog[task], base, target, sha
        if any(status != "Готово" for status in statuses.values()):
            raise Stop("Нет доступных карточек; проверь зависимости")
        return None

    def start(self, selection):
        task, card, base, target, sha = selection
        token = time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:6]
        directory = self.root / "runs" / token
        directory.mkdir(parents=True)
        worker = directory / "worktree"
        branch = f"task/{task}-auto-{token}"
        if base != target and git(self.repo, "rev-parse", "--verify", "refs/heads/" + target, check=False):
            raise Stop(f"Цель {target} уже существует; нужна ручная проверка базы")
        git(self.repo, "worktree", "add", "-b", branch, str(worker), sha)
        # Bring only the approved instructions/plan forward, never prototype code.
        for name in ["AGENTS.md", "Kanban.md", "docs/tasks.md", "docs/delivery.md"]:
            (worker / name).write_text(read_ref(self.repo, self.args.journal_branch, name) + "\n")
        git(worker, "add", "AGENTS.md", "Kanban.md", "docs/tasks.md", "docs/delivery.md")
        git(worker, "commit", "--allow-empty", "-m", f"docs({task}): synchronize task instructions")
        self.state = {"task": task, "base": base, "base_sha": sha, "target": target,
                      "branch": branch, "worker": str(worker), "directory": str(directory),
                      "journal_branch": self.args.journal_branch,
                      "phase": "agent", "attempt": 0, "session": None, "error": "",
                      "card": card["body"], "usage": []}
        self.save()

    def agent(self):
        s = self.state
        s["attempt"] += 1
        attempt = Path(s["directory"]) / f"attempt-{s['attempt']}"
        attempt.mkdir()
        schema = attempt / "schema.json"
        atomic_json(schema, SCHEMA)
        result = attempt / "result.json"
        logfile = attempt / "events.jsonl"
        s["result"] = str(result)
        self.save()
        prompt = f"""Пользователь поручил только {s['task']} в автоматической очереди.
Прочитай AGENTS.md, docs/stages.md, docs/delivery.md и карточку ниже.
Работай ТОЛЬКО в существующей task-ветке {s['branch']}, worktree {s['worker']}.
База: {s['base']} на {s['base_sha']}; цель после проверки: {s['target']}.
Особый порядок этой очереди: подготовь коммиты, но НЕ интегрируй результат и
НЕ изменяй общий журнал в других worktree: это сделает контроллер после проверок.
В своей копии канбана пометь карточку «В работе», после всех проверок «Готово».
Не запускай следующие карточки, других агентов, push и изменения main.
Выполни критерии карточки и обязательные build/test/race/vet или lint/gofmt.
При недоступной обязательной проверке верни blocked, а не done.
При done рабочее дерево чистое, commit — полный SHA HEAD, limitations — пустая
строка; extra_checks — фактические результаты дополнительных проверок карточки.
Обнови docs/delivery.md в своей task-ветке: поведение, файлы и реальные результаты.
При blocked/failed/needs_input укажи причину; commit может быть пустой строкой.
Не пытайся исправлять сам контроллер или файлы его состояния.
Предыдущее препятствие: {s['error'] or 'нет'}
{s['card']}
"""
        (attempt / "prompt.txt").write_text(prompt)
        command = ["codex", "exec", "-C", s["worker"], "--sandbox", self.args.sandbox]
        if s["session"]:
            command += ["resume", s["session"]]
        command += ["--json", "--output-schema", str(schema), "-o", str(result), prompt]
        print(f"{s['task']}: агент; лог {logfile}", flush=True)
        try:
            code = execute(command, s["worker"], logfile, self.args.task_timeout)
        finally:
            session, usage, healthy = events(logfile)
            s["session"] = session or s["session"]
            s["usage"].append(usage)
            self.save()
        if code != 0 or not healthy:
            raise Stop(f"Ошибка Codex (exit={code}); см. {logfile}")
        try:
            report = json.loads(result.read_text())
        except (OSError, ValueError) as error:
            raise Stop(f"Нет корректного итогового JSON: {error}") from None
        validate_result(report, s["task"])
        s["phase"] = "verify"
        self.save()

    def verify(self):
        s = self.state
        report = json.loads(Path(s["result"]).read_text())
        validate_result(report, s["task"])
        sha = report["commit"]
        worker = Path(s["worker"])
        clean(worker)
        if git(worker, "branch", "--show-current") != s["branch"] or git(worker, "rev-parse", "HEAD") != sha:
            raise Stop("HEAD/ветка worktree не совпадают с результатом агента")
        git(worker, "merge-base", "--is-ancestor", s["base_sha"], sha)
        if board((worker / "Kanban.md").read_text()).get(s["task"]) != "Готово":
            raise Stop("Карточка не отмечена готовой в результате")
        checks = [["go", "build", "./..."], ["go", "test", "-count=1", "./..."],
                  ["go", "test", "-race", "-count=1", "./..."],
                  ["golangci-lint", "run", "./..."] if (worker / ".golangci.yml").exists()
                  else ["go", "vet", "./..."], ["gofmt", "-l", "."], ["git", "diff", "--check"]]
        for index, command in enumerate(checks):
            logfile = Path(s["directory"]) / f"check-{index}.log"
            print(f"{s['task']}: {' '.join(command)}", flush=True)
            code = execute(command, worker, logfile, self.args.check_timeout)
            if code or (command[0] == "gofmt" and logfile.read_text().strip()):
                raise Stop(f"Проверка не пройдена (exit={code}): {logfile}")
        clean(worker)
        if git(worker, "rev-parse", "HEAD") != sha:
            raise Stop("HEAD изменился во время проверок")
        s["phase"] = "integrate"
        self.save()

    def finish(self):
        s = self.state
        sha = json.loads(Path(s["result"]).read_text())["commit"]
        target = s["target"]
        current = git(self.repo, "rev-parse", "--verify", "refs/heads/" + target, check=False)
        if current != sha:
            expected = s["base_sha"] if target == s["base"] else ""
            if current != expected:
                raise Stop(f"Цель {target} изменилась; интеграция остановлена")
            checkout = worktrees(self.repo).get(target)
            if checkout:
                clean(checkout)
                git(checkout, "merge", "--ff-only", sha)
            else:
                git(self.repo, "update-ref", "refs/heads/" + target, sha, current or "0" * 40)
        journal = self.journal()
        marker = f"<!-- autokanban:{s['task']}:{sha} -->"
        delivery = journal / "docs/delivery.md"
        if marker not in delivery.read_text():
            statuses = board((journal / "Kanban.md").read_text())
            if statuses.get(s["task"]) != "К выполнению":
                raise Stop("Общий статус карточки изменён другим процессом; нужна сверка")
            report = json.loads(Path(s["result"]).read_text())
            (journal / "Kanban.md").write_text(move_card((journal / "Kanban.md").read_text(), s["task"], "Готово"))
            delivery.write_text(delivery.read_text() + f"\n\n## {s['task']} — автоматическая очередь\n\n"
                                f"{marker}\n\nВетка `{s['branch']}`, база `{s['base']}` на `{s['base_sha']}`.\n"
                                f"Результат `{sha}` проверен контроллером и интегрирован в `{target}`.\n"
                                f"Build/test/race/vet или lint/gofmt/diff-check: PASS.\n"
                                f"Дополнительные проверки (отчёт исполнителя): {report['extra_checks']}\n"
                                f"Подробный отчёт и изменённые файлы: docs/delivery.md в task-ветке.\n"
                                f"Логи контроллера: `{s['directory']}`. Push не выполнялся.\n")
            git(journal, "add", "Kanban.md", "docs/delivery.md")
            git(journal, "commit", "-m", f"docs({s['task']}): record verified automatic delivery")
        s["phase"] = "completed"
        s["error"] = ""
        self.save()
        atomic_json(Path(s["directory"]) / "state.json", s)
        print(f"{s['task']}: ГОТОВО → {target} {sha[:12]}", flush=True)

    def run(self, resume):
        if self.state and self.state["phase"] != "completed":
            if not resume:
                raise Stop("Есть незавершённый запуск; используй status и resume")
            if self.state["journal_branch"] != self.args.journal_branch:
                raise Stop("Ветка журнала отличается от сохранённой")
        elif resume:
            raise Stop("Нет незавершённой карточки для resume; используй run")
        if resume and self.args.repair:
            self.state["phase"] = "agent"
            self.save()
        for _ in range(self.args.max_tasks):
            self.journal()
            if not self.state or self.state["phase"] == "completed":
                selection = self.select()
                if selection is None:
                    print("Все карточки готовы")
                    return
                self.start(selection)
            if self.state["phase"] == "agent":
                self.agent()
            # Reverify the exact commit even after an interrupted integration.
            self.verify()
            self.finish()


def main():
    parser = argparse.ArgumentParser(prog="autokanban", description="Последовательная очередь raftgo; stop on failure")
    parser.add_argument("command", choices=["plan", "run", "resume", "status"])
    parser.add_argument("--repo", default=str(Path(__file__).resolve().parents[2]))
    parser.add_argument("--journal-branch", default="prepare/in-memory")
    parser.add_argument("--max-tasks", type=int, default=1)
    parser.add_argument("--task-timeout", type=int, default=7200)
    parser.add_argument("--check-timeout", type=int, default=1200)
    parser.add_argument("--sandbox", choices=["workspace-write", "danger-full-access"], default="workspace-write")
    parser.add_argument("--repair", action="store_true", help="При resume вернуть текущую карточку агенту для исправления")
    args = parser.parse_args()
    if min(args.max_tasks, args.task_timeout, args.check_timeout) < 1:
        parser.error("Количество задач и таймауты должны быть положительными")
    if args.repair and args.command != "resume":
        parser.error("--repair используется только с resume")
    runner = Runner(args)
    if args.command == "status":
        print(json.dumps({"state_file": str(runner.statefile), **(runner.state or {"phase": "idle"})},
                         ensure_ascii=False, indent=2))
        return 0
    with (runner.root / "lock").open("w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            print("ОСТАНОВКА: другой контроллер уже запущен", file=sys.stderr)
            return 1
        # Reload under lock: another process may have completed during startup.
        runner.state = json.loads(runner.statefile.read_text()) if runner.statefile.exists() else None
        try:
            if args.command == "plan":
                selection = runner.select()
                print(f"Следующая: {selection[0]}, база {selection[2]}, цель {selection[3]}" if selection else "Все карточки готовы")
            else:
                runner.run(args.command == "resume")
        except (Stop, OSError, ValueError) as error:
            if runner.state and runner.state["phase"] != "completed" and args.command in {"run", "resume"}:
                runner.state["error"] = str(error)
                runner.save()
            print(f"ОСТАНОВКА: {error}\nСостояние и логи: {runner.root}", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    def interrupted(signum, frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    sys.exit(main())
