#!/usr/bin/env python3
"""Запуск пяти узлов и пяти окон Terminal с их логами."""
import argparse
import pathlib
import shlex
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]


def commands(root, project):
    compose = ["docker", "compose", "-p", project, "-f", str(root / "compose.yaml")]
    windows = []
    for number in range(1, 6):
        node = f"node{number}"
        shell = f"cd {shlex.quote(str(root))} && " + shlex.join(
            compose + ["logs", "--follow", "--tail", "80", node]
        )
        windows.append(shell)
    return compose, windows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=pathlib.Path, default=ROOT,
                        help="папка проекта (по умолчанию — папка этого скрипта)")
    parser.add_argument("--project", default="raftgo-scenarios", help="имя проекта Docker Compose")
    parser.add_argument("--dry-run", action="store_true", help="показать команды без запуска")
    args = parser.parse_args()
    root = args.directory.resolve()
    compose_file = root / "compose.yaml"
    if not compose_file.is_file():
        parser.error(f"Файл не найден: {compose_file}")
    compose, windows = commands(root, args.project)
    if args.dry_run:
        print(shlex.join(compose + ["up", "-d", "--build"]))
        for shell in windows:
            print(shell)
        return
    if sys.platform != "darwin":
        parser.error("Для открытия окон нужен macOS Terminal. Команды можно посмотреть через --dry-run.")
    result = subprocess.run(
        compose + ["ps", "--all", "--quiet"],
        check=True, text=True, capture_output=True,
    )
    if result.stdout.strip():
        print("Контейнеры уже существуют. Открываю логи без перезапуска.")
    else:
        subprocess.run(compose + ["up", "-d", "--build"], check=True)

    terminal_script = '''on run argv
    tell application "Terminal"
        activate
        repeat with shellCommand in argv
            do script (contents of shellCommand)
        end repeat
    end tell
end run'''
    subprocess.run(["osascript", "-e", terminal_script, *windows], check=True)
    print("UI: http://localhost:18001/   Swagger: http://localhost:18001/swagger/")
    print("Закрытие окна останавливает только просмотр логов, узел продолжает работать.")
    print("Остановить весь стенд и удалить данные из RAM: " + shlex.join(compose + ["down"]))


if __name__ == "__main__":
    try:
        main()
    except (subprocess.CalledProcessError, OSError) as error:
        sys.exit(f"Не удалось запустить демонстрацию: {error}")
