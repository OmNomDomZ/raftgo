#!/usr/bin/env python3
"""Start the demo cluster and open five macOS Terminal windows with node logs."""
import argparse
import json
import pathlib
import shlex
import subprocess
import sys

ROOT = pathlib.Path(__file__).resolve().parents[1]


def commands(root, project):
    compose = ["docker", "compose", "-p", project, "-f", str(root / "scenarios/compose.yaml")]
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
                        help="checkout with scenarios/compose.yaml (default: script's project)")
    parser.add_argument("--project", default="raftgo-scenarios", help="Docker Compose project")
    parser.add_argument("--dry-run", action="store_true", help="print commands without starting anything")
    args = parser.parse_args()
    root = args.directory.resolve()
    if not (root / "scenarios/compose.yaml").is_file():
        parser.error(f"missing {root / 'scenarios/compose.yaml'}")
    compose, windows = commands(root, args.project)
    if args.dry_run:
        print(shlex.join(compose + ["up", "-d", "--build"]))
        for shell in windows:
            print(shell)
        return
    if sys.platform != "darwin":
        parser.error("opening windows requires macOS Terminal.app; use --dry-run to see commands")
    # Do not change existing clusters: up could otherwise return an
    # empty-memory node to an existing cluster. A complete down is an explicit reset.
    result = subprocess.run(compose + ["ps", "--all", "--format", "json"],
                            check=True, text=True, capture_output=True)
    text = result.stdout.strip()
    containers = json.loads(text) if text.startswith("[") else [json.loads(line) for line in text.splitlines()]
    if containers:
        print("Cluster already exists. Following its logs without rebuilding or restarting nodes.")
    else:
        subprocess.run(compose + ["up", "-d", "--build"], check=True)
    # Pass shell commands as argv; never interpolate shell text into AppleScript.
    apple = '''on run argv
    tell application "Terminal"
        activate
        repeat with shellCommand in argv
            do script (contents of shellCommand)
        end repeat
    end tell
end run'''
    subprocess.run(["osascript", "-e", apple, *windows], check=True)
    print("UI: http://localhost:18001/   Swagger: http://localhost:18001/swagger/")
    print("Closing a window stops log viewing only. Model failures with compose pause/unpause.")
    print("Full cluster reset (loses all RAM state): " + shlex.join(compose + ["down"]))


if __name__ == "__main__":
    try:
        main()
    except (subprocess.CalledProcessError, OSError) as error:
        sys.exit(f"Demo launch failed: {error}")
