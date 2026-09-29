"""Writes golden fixtures for internal/testsfile from the Python loader scripts/collect.py.

Run: cd ~/jev-ultrafast && uv run python ~/pagevow/scripts/golden/testsfile/generate.py
"""

import json
import os
import shutil
import sys
from datetime import date
from pathlib import Path

JEV = Path(os.environ.get("JEV_ROOT", Path.cwd()))
OUT = Path(__file__).resolve().parents[3] / "internal" / "testsfile" / "testdata"
DEMO = Path(os.environ.get("DEMO_TESTS", Path.home() / "browser-test-demo" / "browser-tests.yaml"))
TODAY = date(2026, 9, 28)

sys.path.insert(0, str(JEV))
from scripts import collect  # noqa: E402


def listify(value):
    if value is None:
        return []
    return [value] if isinstance(value, str) else list(value)


def scalar(value):
    kinds = {bool: "bool", int: "int", float: "float", type(None): "null", str: "string"}
    return {"type": kinds.get(type(value), type(value).__name__), "text": str(value)}


def canonical(verifier, args):
    if verifier == "page":
        return {
            "url": listify(args.get("url")), "text": listify(args.get("text")),
            "fields": [[label, scalar(want)] for label, want in (args.get("fields") or {}).items()],
            "values": [scalar(value) for value in listify(args.get("values"))],
            "checked": [[label, count] for label, count in (args.get("checked") or {}).items()],
        }
    if verifier == "echo":
        return {"url": listify(args["url"]), "values": listify(args["values"])}
    if verifier == "hn_story":
        return {"rank": args["rank"], "comments": bool(args.get("comments", False))}
    return {
        "origin": args["origin"], "destination": args["destination"], "day": args["day"].isoformat(),
        "return_day": args["return_day"].isoformat() if "return_day" in args else None,
        "one_way": args.get("one_way", True), "adults": args.get("adults"),
    }


def export(source, name):
    tasks = collect.load_tasks(source, TODAY)
    rows = [
        {
            "id": task["id"], "url": task["url"], "goal": task["goal"], "tags": task["tags"],
            "verify": task.get("verify"), "repeat": task.get("repeat"),
            "args": canonical(task["verify"], task["verify_args"]) if "verify" in task else None,
        }
        for task in tasks
    ]
    (OUT / name).write_text(json.dumps(rows, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{name}: {len(rows)}")


if __name__ == "__main__":
    OUT.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(JEV / "tasks.yaml", OUT / "tasks.yaml")
    shutil.copyfile(DEMO, OUT / "demo-browser-tests.yaml")
    export(OUT / "tasks.yaml", "tasks_resolved.json")
    export(OUT / "demo-browser-tests.yaml", "demo_resolved.json")
    (OUT / "today.txt").write_text(TODAY.isoformat() + "\n")
