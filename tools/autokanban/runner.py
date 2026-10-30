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
        if self.state and self.state.get("version") != 2:
            raise Stop("Старый формат состояния: завершите или разберите прежний запуск вручную")

    def save(self):
        atomic_json(self.statefile, self.state)

    def journal(self):
        if git(self.repo, "branch", "--show-current") != "main":
            raise Stop("Нужен checkout main; контроллер не переключает ветки")
        clean(self.repo)
        return self.repo

    def lab_refs(self):
        return git(self.repo, "for-each-ref", "--format=%(refname) %(objectname)",
                   "refs/heads/lab[1-4]")

    def select(self):
        text = read_ref(self.repo, "main", "Kanban.md")
        statuses = board(text)
        catalog = cards(read_ref(self.repo, "main", "docs/tasks.md"))
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
            if not re.fullmatch(r"S[1-4]-(CORE|API|UI|CLIENT|DEMO)", task):
                raise Stop(f"Первая версия поддерживает только S1–S4: {task}")
            if task.endswith("-DEMO"):
                raise Stop(f"Достигнута {task}: приёмка этапа запускается вручную")
            sha = git(self.repo, "rev-parse", "--verify", "refs/heads/main")
            return task, catalog[task], "main", "main", sha
        if any(status != "Готово" for status in statuses.values()):
            raise Stop("Нет доступных карточек; проверь зависимости")
        return None

    def start(self, selection):
        task, card, base, target, sha = selection
        token = time.strftime("%Y%m%d-%H%M%S") + "-" + uuid.uuid4().hex[:6]
        directory = self.root / "runs" / token
        directory.mkdir(parents=True)
        self.journal()
        if git(self.repo, "rev-parse", "HEAD") != sha:
            raise Stop("main изменилась после выбора карточки")
        self.state = {"version": 2, "task": task, "base": base, "base_sha": sha,
                      "target": target, "branch": "main", "worker": str(self.repo),
                      "directory": str(directory), "journal_branch": "main",
                      "phase": "agent", "attempt": 0, "session": None, "error": "",
                      "card": card["body"], "usage": [], "lab_refs": self.lab_refs(),
                      "board": board(read_ref(self.repo, "main", "Kanban.md"))}
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
Работай ТОЛЬКО в существующей ветке {s['branch']}, папке {s['worker']}.
База: main на {s['base_sha']}. Разработка, канбан и журнал находятся в main.
Не создавай ветки, worktree и копии проекта, не переключай checkout.
Не изменяй lab1–lab4: их снимки готовятся вручную в DEMO.
Пометь карточку «В работе», после всех проверок «Готово».
Ограничения будущих функций относятся к снимкам labN; сохраняй полный код main.
Не запускай следующие карточки, других агентов или push. Не переписывай историю main.
Выполни критерии карточки и обязательные build/test/race/vet или lint/gofmt.
При недоступной обязательной проверке верни blocked, а не done.
При done рабочее дерево чистое, commit — полный SHA HEAD, limitations — пустая
строка; extra_checks — фактические результаты дополнительных проверок карточки.
Обнови docs/delivery.md в main: поведение, файлы и реальные результаты.
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
            raise Stop("HEAD/ветка checkout не совпадают с результатом агента")
        git(worker, "merge-base", "--is-ancestor", s["base_sha"], sha)
        statuses = board((worker / "Kanban.md").read_text())
        expected = {**s["board"], s["task"]: "Готово"}
        if statuses != expected:
            raise Stop("Изменены чужие карточки или своя не отмечена готовой")
        if self.lab_refs() != s["lab_refs"]:
            raise Stop("Ссылки lab1–lab4 изменены во время карточки")
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
        s["phase"] = "verified"
        self.save()

    def finish(self):
        s = self.state
        sha = json.loads(Path(s["result"]).read_text())["commit"]
        self.journal()
        if git(self.repo, "rev-parse", "HEAD") != sha or self.lab_refs() != s["lab_refs"]:
            raise Stop("main или снимки изменились после проверок")
        s["phase"] = "completed"
        s["error"] = ""
        self.save()
        atomic_json(Path(s["directory"]) / "state.json", s)
        print(f"{s['task']}: ПРОВЕРЕНО → main {sha[:12]}", flush=True)

    def run(self, resume):
        if self.state and self.state["phase"] != "completed":
            if not resume:
                raise Stop("Есть незавершённый запуск; используй status и resume")
            if self.state["journal_branch"] != "main":
                raise Stop("Ветка журнала отличается от сохранённой")
        elif resume:
            raise Stop("Нет незавершённой карточки для resume; используй run")
        if resume and self.args.repair:
            self.state["phase"] = "agent"
            self.save()
        for _ in range(self.args.max_tasks):
            # A failed agent may leave edits; resume must let it repair those edits.
            if self.state and self.state["phase"] == "agent":
                if git(self.repo, "branch", "--show-current") != "main":
                    raise Stop("Нужен checkout main")
            else:
                self.journal()
            if not self.state or self.state["phase"] == "completed":
                selection = self.select()
                if selection is None:
                    print("Все карточки готовы")
                    return
                self.start(selection)
            if self.state["phase"] == "agent":
                self.agent()
            # Reverify the exact commit after any interruption.
            self.verify()
            self.finish()


def main():
    parser = argparse.ArgumentParser(prog="autokanban", description="Последовательная очередь raftgo; stop on failure")
    parser.add_argument("command", choices=["plan", "run", "resume", "status"])
    parser.add_argument("--repo", default=str(Path(__file__).resolve().parents[2]))
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
    try:
        runner = Runner(args)
    except (Stop, OSError, ValueError) as error:
        print(f"ОСТАНОВКА: {error}", file=sys.stderr)
        return 1
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
        if runner.state and runner.state.get("version") != 2:
            print("ОСТАНОВКА: старый формат состояния; нужен ручной разбор", file=sys.stderr)
            return 1
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
