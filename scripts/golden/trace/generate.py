"""Writes trace fixtures from the Python reference: for each scripted scenario, the script and the exact files the
Python Trace writes. Time and randomness are pinned, so nothing needs normalising. Run from ~/jev-ultrafast:
uv run python /home/aymane/pagevow/scripts/golden/trace/generate.py"""

import json
import os
import secrets
import tempfile
import time
from pathlib import Path
from unittest import mock

from jev_ultrafast.tracing import Trace

OUT = Path(__file__).resolve().parents[3] / "internal" / "trace" / "testdata"
RUN_ID = "20260929T120000-ab12"
GOAL = "On the contact page, enter Zoë Müller <zoe@example.com> & stop. 日本語"
URL = "http://localhost:3000/contact.html"


def request(label):
    return {
        "model": "jev-latest",
        "state": {"page": {"url": URL, "title": "Contact", "text": "Name\nEmail"}, "elements": [f"[1] {label}"]},
        "questions": {"operation": {"type": "choice", "criteria": {"1": "Click", "2": "Done"}}},
    }


def result(operation, tokens=100, usage=True):
    answers = {"operation": {"choice": operation, "probabilities": {"CLICK": 0.0625, "DONE": 0.9375}, "confidence": 0.9}}
    reply = {"model": "jev-4b", "answers": answers}
    if usage:
        reply["usage"] = {"input_tokens": tokens, "output_tokens": 12}
    return reply


def field_context(label, recent):
    return {
        "goal": GOAL,
        "field": {"label": label, "role": "textbox", "value": ""},
        "page": {"title": "Contact - Shelf", "text": "Shelf\nContact us\nName\nEmail"},
        "recent_actions": recent,
    }


def start(t_ms):
    return {"op": "start_step", "t_ms": t_ms, "url": URL}


def phase(name, seconds, fail=None):
    return {"op": "phase", "name": name, "seconds": seconds, "fail": fail}


def step(operation, latency, **extra):
    return {
        "op": "step",
        "goal": GOAL,
        "request": request(operation),
        "result": extra.pop("result", result(operation)),
        "latency_ms": latency,
        "awaiting_text": extra.pop("awaiting_text", False),
        "retries": extra.pop("retries", None),
        "cascade": extra.pop("cascade", None),
    }


def finish(status, elapsed_ms, error=None, raw_response=None, reason=None):
    return {
        "op": "finish", "status": status, "elapsed_ms": elapsed_ms,
        "error": error, "raw_response": raw_response, "reason": reason,
    }


SCENARIOS = {
    "normal_run": [
        start(0),
        phase("snapshot", 0.0625),
        phase("model", 0.5),
        step("TYPE_TEXT", 533, awaiting_text=True),
        phase("text", 1.625),
        {"op": "type_text", "context": field_context("Name", []), "text": "Zoë <Müller> & \"Co\""},
        phase("execute", 0.1875),
        phase("wait", 0.03125),
        {"op": "end_step", "error": None},
        start(2265),
        phase("snapshot", 0.0),
        phase("model", 0.640625),
        step("CLICK", 639),
        phase("execute", 0.046875),
        phase("wait", 0.015625),
        {"op": "end_step", "error": None},
        start(4130),
        phase("model", 0.25),
        phase("model", 0.125),
        step("DONE", 662, result=result("DONE", 200, usage=False)),
        phase("execute", 0.0),
        {"op": "end_step", "error": None},
        finish("DONE", 7464),
        {"op": "set_verified", "value": True},
    ],
    "failed_steps": [
        start(0),
        phase("snapshot", 0.0625),
        phase("model", 0.5),
        step("CLICK", 500),
        phase("execute", 0.125),
        phase("wait", 0.25),
        {"op": "end_step", "error": None},
        start(1200),
        phase("snapshot", 0.0625),
        phase("model", 0.75, fail="model connection failed"),
        {"op": "end_step", "error": "model connection failed"},
        start(2500),
        phase("model", 0.5),
        step("CLICK", 480),
        phase("execute", 0.375, fail="CDP call timed out"),
        {"op": "end_step", "error": "CDP call timed out"},
        finish("error", 4100, error="CDP call timed out"),
        {"op": "set_verified", "value": False},
    ],
    "failed_after_a_finished_phase": [
        start(0),
        phase("model", 0.5),
        step("CLICK", 500),
        {"op": "end_step", "error": "Stopped at the 60-action demo budget"},
        finish("max_steps", 900, error="Stopped at the 60-action demo budget"),
    ],
    "retry": [
        start(0),
        phase("snapshot", 0.0625),
        phase("model", 0.5),
        phase("model", 1.5),
        step(
            "CLICK", 720,
            retries=[{"error": "Invalid TypeSafe response; no action executed.", "after_ms": 512,
                      "raw": '{"model": "jev-4b", "answers": {}}'}],
        ),
        phase("execute", 0.03125),
        phase("wait", 0.0),
        {"op": "end_step", "error": None},
        start(2400),
        phase("model", 0.25),
        step("CLICK", 600, retries=[{"error": "Model connection failed; no action executed.", "after_ms": 250}]),
        phase("execute", 0.0),
        {"op": "end_step", "error": None},
        start(3100),
        phase("model", 0.25),
        step("DONE", 610, retries=[]),
        {"op": "end_step", "error": None},
        finish("DONE", 3900),
        {"op": "set_verified", "value": True},
    ],
    "cascade_and_gates": [
        start(0),
        phase("model", 0.75),
        step(
            "DONE", 900,
            cascade={
                "reason": "done", "used": "verifier",
                "primary": {"answers": result("DONE")["answers"], "model": "jev-08b", "latency_ms": 120},
                "verifier": {"answers": result("CLICK")["answers"], "model": "jev-4b", "latency_ms": 780},
            },
        ),
        {"op": "gate", "note": {"from": "DONE", "probability": 0.4, "threshold": 0.5, "to": "CLICK", "to_probability": 0.3},
         "operation": "DONE"},
        {"op": "loop_guard", "note": {"pattern": "cycle", "replaced": "Next", "with": "Prev", "probability": 0.2}},
        phase("execute", 0.0625),
        {"op": "end_step", "error": None},
        start(1100),
        phase("model", 0.5),
        step("BLOCKED", 300, cascade={"reason": "low_conf", "used": "cache", "primary": {"model": "jev-08b"}}),
        {"op": "gate", "note": {"from": "BLOCKED", "probability": 0.1}, "operation": "BLOCKED"},
        {"op": "end_step", "error": None},
        finish("BLOCKED", 2000, reason="Target refused 2 times: Send: not editable"),
        {"op": "set_verified", "value": False},
    ],
    "usage_omitted_null_or_empty": [
        step("CLICK", 100, result=result("CLICK", usage=False)),
        step("CLICK", 110, result={**result("CLICK"), "usage": None}),
        step("CLICK", 120, result={**result("CLICK"), "usage": {}}),
        step("DONE", 130, result=result("DONE", 7)),
        finish("DONE", 10),
    ],
    "closed_without_running": [
        finish("closed", 0),
        {"op": "set_verified", "value": False},
        {"op": "set_verified", "value": None},
        {"op": "set_verified", "value": True},
    ],
    "abandoned": [
        start(0),
        phase("model", 0.5),
        step("TYPE_TEXT", 400, awaiting_text=True),
        {"op": "gate", "note": {"from": "TYPE_TEXT"}, "operation": "TYPE_TEXT"},
    ],
    "untimed_step": [
        step("CLICK", 101),
        step("CLICK", 200, awaiting_text=True),
        {"op": "type_text", "context": field_context("City", [{"action": "Name", "text": "x"}]), "text": "Zurich"},
        step("DONE", 150),
        {"op": "gate", "note": {"from": "DONE"}, "operation": "DONE"},
        finish("DONE", 10),
        {"op": "end_step", "error": None},
        finish("ERROR", 20, error="ignored"),
    ],
}


class Clock:
    def __init__(self):
        self.now = 1000.0

    def perf_counter(self):
        return self.now


def run(name, ops):
    clock = Clock()
    with tempfile.TemporaryDirectory() as directory:
        env = mock.patch.dict(os.environ, {"TRACE_DIR": directory})
        with env, mock.patch.object(time, "perf_counter", clock.perf_counter), \
                mock.patch.object(time, "strftime", lambda _fmt: RUN_ID.split("-")[0]), \
                mock.patch.object(secrets, "token_hex", lambda _n: RUN_ID.split("-")[1]):
            trace = Trace(URL, GOAL)
            assert trace.run_id == RUN_ID
            for op in ops:
                kind = op["op"]
                if kind == "start_step":
                    trace.start_step(op["t_ms"], op["url"])
                elif kind == "phase":
                    try:
                        with trace.timed(op["name"]):
                            clock.now += op["seconds"]
                            if op["fail"]:
                                raise RuntimeError(op["fail"])
                    except RuntimeError:
                        pass
                elif kind == "step":
                    trace.step(
                        op["goal"], op["request"], op["result"], op["latency_ms"],
                        awaiting_text=op["awaiting_text"], retries=op["retries"], cascade=op["cascade"],
                    )
                elif kind == "type_text":
                    trace.type_text(op["context"], op["text"])
                elif kind == "gate":
                    gated = {"confidence_gate": op["note"], f"{op['operation'].lower()}_gated": True}
                    if trace.pending is not None:
                        trace.pending.update(gated)
                elif kind == "loop_guard":
                    if trace.pending is not None:
                        trace.pending["loop_guard"] = op["note"]
                elif kind == "end_step":
                    trace.end_step(None if op["error"] is None else RuntimeError(op["error"]))
                elif kind == "finish":
                    trace.finish(op["status"], op["elapsed_ms"], op["error"], op["raw_response"], op["reason"])
                elif kind == "set_verified":
                    trace.set_verified(op["value"])
                else:
                    raise ValueError(kind)
            stats = trace.stats()
        files = {}
        for suffix in (".jsonl", ".meta.json"):
            path = Path(directory) / f"{RUN_ID}{suffix}"
            files[suffix] = path.read_bytes() if path.exists() else None
    (OUT / f"{name}.script.json").write_text(
        json.dumps({"name": name, "url": URL, "goal": GOAL, "ops": ops}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )
    for suffix, data in files.items():
        target = OUT / f"{name}.expected{suffix}"
        if data is None:
            target.unlink(missing_ok=True)
        else:
            target.write_bytes(data)
    (OUT / f"{name}.expected.stats.json").write_text(json.dumps(stats, indent=2) + "\n", encoding="utf-8")
    return files


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for name, ops in SCENARIOS.items():
        files = run(name, ops)
        lines = files[".jsonl"].decode().count("\n") if files[".jsonl"] else 0
        print(f"{name}: {lines} lines, meta={'yes' if files['.meta.json'] else 'no'}")


if __name__ == "__main__":
    main()
