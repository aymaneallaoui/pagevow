"""Writes agent scenarios from the Python reference. Run from ~/jev-ultrafast:
uv run python /home/aymane/pagevow/scripts/golden/agent/generate.py

Each scenario drives the Python Agent with a scripted browser, a stubbed model and text helper, and a fake clock, then
stores the scripted inputs and the observed outcome. No network, no browser.
"""

import copy
import json
import os
import tempfile
import time
from pathlib import Path

from jev_ultrafast import agent as loop
from jev_ultrafast import model
from jev_ultrafast.browser import StalePage, TargetRefused, fingerprint

OUT = Path(__file__).resolve().parents[3] / "internal" / "agent" / "testdata"
URL = "https://example.test/"
STALE = "Page changed since this decision. Observe again."
REFUSED = "Target changed or is covered. Observe again."
ENV_KEYS = (
    "TRACE_DIR", "TYPESAFE_API_KEY", "TYPESAFE_BASE_URL", "TYPESAFE_MODEL", "TEXT_MODEL_API_KEY", "TEXT_MODEL",
    "TEXT_MODEL_BASE_URL", "TEXT_MODEL_REASONING", "TEXT_TIMEOUT_S", "JEV_LOOP_GUARD", "JEV_DONE_MIN_CONF",
    "JEV_BLOCKED_MIN_CONF", "JEV_VERIFIER_BASE_URL", "JEV_VERIFIER_API_KEY", "JEV_VETO_CACHE",
    "JEV_CASCADE_TARGET_CONF",
)
TIMING_KEYS = {"t_ms", "snapshot_ms", "model_ms", "execute_ms", "wait_ms", "text_ms", "after_ms"}
HISTORY_TIMING_KEYS = {"elapsed_ms", "executed_ms", "text_latency_ms"}


class Clock:
    now = 0.0


def fake_sleep(seconds):
    Clock.now += seconds


time.perf_counter = lambda: Clock.now
loop.clock = lambda: Clock.now
loop.sleep = fake_sleep


def action(id, kind, label, node=None, role=None, value="", **extra):
    item = {"id": id, "kind": kind, "label": label}
    if node is not None:
        item["node"] = node
    if role is not None:
        item["role"] = role
    if kind != "wait":
        item["value"] = value
    item.update(extra)
    return item


WAIT = {"id": "wait", "kind": "wait", "label": "Wait"}


def make_page(text, actions, url=URL, title="Search"):
    state = {"url": url, "title": title, "text": text, "scroll": {"y": 0}, "actions": copy.deepcopy(actions)}
    state["fingerprint"] = fingerprint(state)
    return state


SEARCH = [
    action("e1", "fill", "Search", 10, "textbox"),
    action("e2", "click", "Go", 20, "button"),
    action("e3", "click", "More", 30, "button"),
    WAIT,
]
SEARCH_FILLED = [dict(SEARCH[0], value="book"), *SEARCH[1:]]
MENU = [
    action(f"e{n}", "click", label, n, "button")
    for n, label in enumerate(["Advanced", "Buy It Now", "Price", "Search"], start=1)
] + [WAIT]

P0 = make_page("Search", SEARCH)
P1 = make_page("Results", SEARCH)
P2 = make_page("Details", SEARCH)
PF = make_page("Search book", SEARCH_FILLED)
P0_CHANGED = make_page("Different page context", SEARCH)
M = [make_page(f"Menu {n}", MENU) for n in range(5)]
EMPTY = make_page("", [WAIT])
EMPTY_OTHER = make_page("", [WAIT], url="https://other.test/article")
LOADED_OTHER = make_page("Article", SEARCH, url="https://other.test/article")


def combo(expanded, options=0):
    actions = SEARCH[:3] + [action("e4", "click", "Where to?", 40, "combobox", expanded=expanded)]
    actions += [action(f"e{5 + i}", "click", f"City {i}", 50 + i, "option") for i in range(options)]
    return make_page(f"Search {expanded} {options}", actions + [WAIT])


def full(ids, given):
    probabilities = {i: given.get(i, 0.0) for i in ids}
    assert set(given) <= set(ids), (given, ids)
    assert abs(sum(probabilities.values()) - 1) < 0.02, probabilities
    return probabilities


def reply(operation, target=None, op=None, heads=None, order=None, tokens=5):
    """A /v1/systemone response for whatever request arrives; op and heads override probabilities."""

    def build(body):
        questions = body["questions"]
        ids = list(questions["operation"]["criteria"])
        if order:
            ids = [i for i in order if i in ids] + [i for i in ids if i not in order]
        probabilities = full(ids, op or {operation: 1.0})
        answers = {"operation": {"choice": operation, "confidence": probabilities[operation],
                                 "probabilities": probabilities}}
        for name, question in questions.items():
            if name == "operation":
                continue
            ids = list(question["criteria"])
            override = (heads or {}).get(name)
            if override and "raw" in override:
                answers[name] = override["raw"]
                continue
            if override:
                chosen, given = override["choice"], override["probs"]
            else:
                chosen = target if name == operation.lower() + "_target" and target else ids[0]
                given = {chosen: 1.0}
            probabilities = full(ids, given)
            answers[name] = {"choice": chosen, "confidence": probabilities[chosen], "probabilities": probabilities}
        return {"kind": "response",
                "response": {"model": "test", "usage": {"input_tokens": tokens}, "answers": answers}}

    return build


def click(target="2", **kw):
    return reply("CLICK", target, **kw)


def done(**kw):
    return reply("DONE", **kw)


def blocked(**kw):
    return reply("BLOCKED", **kw)


def wait(**kw):
    return reply("WAIT", **kw)


def type_text(target="1", **kw):
    return reply("TYPE_TEXT", target, **kw)


def invalid(pad=0):
    answer = {"choice": "invented"}
    if pad:
        answer["note"] = "x" * pad
    return {"kind": "response", "response": {"model": "test", "answers": {"operation": answer}}}


CONNECTION_ERROR = {"kind": "connection_error"}


def http_error(status):
    return {"kind": "http_error", "status": status}


def text_ok(value):
    content = json.dumps({"text": value})
    return {"kind": "response", "response": {"choices": [{"message": {"content": content}}],
                                             "usage": {"input_tokens": 3}}}


def text_content(content):
    return {"kind": "response", "response": {"choices": [{"message": {"content": content}}]}}


class Script:
    def __init__(self, primary, verifier, text):
        self.queues = {"primary": list(primary), "verifier": list(verifier), "text": list(text)}
        self.recorded = {"primary": [], "verifier": [], "text": []}

    def post(self, url, key, body, **_):
        name = "text" if url.endswith("/chat/completions") else "verifier" if "verifier" in url else "primary"
        entry = self.queues[name].pop(0)
        spec = entry(body) if callable(entry) else entry
        if spec["kind"] == "response":
            text = json.dumps(spec["response"], ensure_ascii=False)
            self.recorded[name].append({"kind": "response", "body_text": text})
            return json.loads(text)
        self.recorded[name].append(spec)
        if spec["kind"] == "connection_error":
            raise model.ModelConnectionError("Model connection failed; no action executed.")
        raise RuntimeError(f"Model provider returned HTTP {spec['status']}; no action executed.")


def raise_scripted(entry):
    message = entry.get("message", STALE)
    if entry["error"] == "stale":
        raise StalePage(message)
    if entry["error"] == "refused":
        raise TargetRefused(message)
    raise RuntimeError(message)


def as_error(entry):
    if entry in ("stale", "refused"):
        return {"error": entry, "message": STALE if entry == "stale" else REFUSED}
    return entry


class FakeBrowser:
    def __init__(self, observations, fresh, acts, calls):
        self.observations, self.fresh_script, self.acts, self.calls = observations, list(fresh), list(acts), calls
        self.index = 0

    def observe(self, screenshot=True):
        self.calls.append(["observe"])
        entry = self.observations[min(self.index, len(self.observations) - 1)]
        self.index += 1
        if "error" in entry:
            raise_scripted(entry)
        return copy.deepcopy(entry)

    def fresh(self, page, action=None):
        self.calls.append(["fresh"])
        entry = self.fresh_script.pop(0) if self.fresh_script else True
        if isinstance(entry, dict):
            raise_scripted(entry)
        return entry

    def act(self, action, page, text=None):
        self.calls.append(["act", action["id"], text])
        entry = self.acts.pop(0) if self.acts else None
        if entry is not None:
            raise_scripted(entry)

    def close(self):
        pass


def strip_trace(line):
    line = {k: v for k, v in line.items() if k not in TIMING_KEYS}
    if "retries" in line:
        line["retries"] = [{k: v for k, v in r.items() if k != "after_ms"} for r in line["retries"]]
    return line


def run(sc):
    Clock.now = 0.0
    config = {"loop_guard": False, "done_min_conf": 0.0, "blocked_min_conf": 0.0, "max_steps": 60,
              "verifier": False, "veto_cache": True, "target_conf": 0.0, "mask_errors": False, "text_helper": True,
              **sc.get("config", {})}
    for key in ENV_KEYS:
        os.environ.pop(key, None)
    os.environ.update(TYPESAFE_API_KEY="test-key", TYPESAFE_BASE_URL="http://primary")
    if config["text_helper"]:
        os.environ["TEXT_MODEL_API_KEY"] = "text-key"
    if config["loop_guard"]:
        os.environ["JEV_LOOP_GUARD"] = "1"
    if config["done_min_conf"]:
        os.environ["JEV_DONE_MIN_CONF"] = str(config["done_min_conf"])
    if config["blocked_min_conf"]:
        os.environ["JEV_BLOCKED_MIN_CONF"] = str(config["blocked_min_conf"])
    if config["verifier"]:
        os.environ["JEV_VERIFIER_BASE_URL"] = "http://verifier"
    if not config["veto_cache"]:
        os.environ["JEV_VETO_CACHE"] = "0"
    if config["target_conf"]:
        os.environ["JEV_CASCADE_TARGET_CONF"] = str(config["target_conf"])
    calls = []
    script = Script(sc.get("primary", []), sc.get("verifier", []), sc.get("text", []))
    observations = [as_error(o) if isinstance(o, str) else o for o in sc["obs"]]
    fresh = [as_error(f) if isinstance(f, str) else f for f in sc.get("fresh", [])]
    acts = [as_error(a) if isinstance(a, str) else a for a in sc.get("acts", [])]
    with tempfile.TemporaryDirectory() as tmp:
        os.environ["TRACE_DIR"] = tmp
        browser = FakeBrowser(observations, fresh, acts, calls)
        loop.Browser = lambda url: browser
        model.post_json = script.post
        loop.MAX_STEPS = config["max_steps"]
        agent = loop.Agent(URL, sc.get("goal", "Find a book"))
        error = None
        try:
            for _ in agent.run():
                pass
        except Exception as caught:
            error = caught
        run_id = agent.trace.run_id
        lines = [json.loads(line) for line in (Path(tmp) / f"{run_id}.jsonl").read_text().splitlines()]
        meta = json.loads((Path(tmp) / f"{run_id}.meta.json").read_text())
    loop.MAX_STEPS = 60
    history = [{k: v for k, v in h.items() if k not in HISTORY_TIMING_KEYS} for h in agent.state["history"]]
    meta.pop("elapsed_ms")
    return {
        "name": sc["name"],
        "goal": sc.get("goal", "Find a book"),
        "url": URL,
        "config": config,
        "observations": observations,
        "fresh": fresh,
        "acts": acts,
        "model": {"primary": script.recorded["primary"], "verifier": script.recorded["verifier"]},
        "text": script.recorded["text"],
        "expected": {
            "status": meta["status"],
            "error": None if error is None else str(error),
            "blocked_reason": agent.state.get("blocked_reason"),
            "steps": meta["steps"],
            "history": history,
            "browser_calls": calls,
            "trace": [strip_trace(line) for line in lines],
            "meta": meta,
            "model_calls": {name: len(items) for name, items in script.recorded.items()},
        },
    }


SCENARIOS = []


def scenario(name, obs, **kw):
    SCENARIOS.append({"name": name, "obs": obs, **kw})


scenario("click_then_done", [P0, P1], primary=[click(), done()])
scenario("type_text_with_helper", [P0, PF], primary=[type_text(), done()], text=[text_ok("book")])
scenario("text_helper_connection_error_then_success", [P0, PF], primary=[type_text(), done()],
         text=[CONNECTION_ERROR, text_ok("book")])
scenario("text_helper_invalid_reply_then_success", [P0, PF], primary=[type_text(), done()],
         text=[text_content("Thinking: book"), text_ok("book")])
scenario("text_helper_fails_twice", [P0], primary=[type_text()],
         text=[text_content("Thinking: book"), text_content("Thinking: book")])
scenario("text_helper_permanent_error_is_not_retried", [P0], primary=[type_text()], text=[http_error(401)])
scenario("text_helper_missing", [P0], primary=[type_text()], config={"text_helper": False})
scenario("text_reused_after_stale_retry", [P0, P0, PF], primary=[type_text(), type_text(), done()],
         acts=["stale", None], text=[text_ok("book")])
scenario("text_regenerated_when_context_changes", [P0, P0_CHANGED, PF], primary=[type_text(), type_text(), done()],
         acts=["stale", None], text=[text_ok("book"), text_ok("novel")])
scenario("stale_page_before_acting", [P0, P0, P1], primary=[click(), click(), done()], acts=["stale", None])
scenario("stale_page_before_deciding", [P0, P1], primary=[done()], fresh=[False])
scenario("stale_navigation_during_prediction", [P0, P0], primary=[done()],
         fresh=[{"error": "stale", "message": "Document navigating"}])
scenario("stale_observation_keeps_the_executed_action", [P0, "stale", P1], primary=[click(), done()])
scenario("target_refused_once_then_success", [P0, P0, P1], primary=[click(), click(), done()],
         acts=["refused", None])
scenario("target_refused_three_times_blocks", [P0], primary=[click(), click(), click()],
         acts=["refused", "refused", "refused"])
scenario("refusal_streak_reset_by_success", [P0, P0, P0, P1], primary=[click()] * 6,
         acts=["refused", "refused", None, "refused", "refused", "refused"])
scenario("three_actions_without_page_change", [P0], primary=[click(), click(), click()])
scenario("waits_do_not_count_as_no_progress", [P0], primary=[wait()] * 5 + [done()])
scenario("blocked_by_the_model", [P0], primary=[blocked()])
scenario("transient_model_error_then_success", [P0, P1], primary=[invalid(), click(), done()])
scenario("connection_error_then_success", [P0, P1], primary=[CONNECTION_ERROR, click(), done()])
scenario("invalid_response_retry_keeps_the_raw_reply", [P0, P1], primary=[invalid(3000), click(), done()])
scenario("two_connection_errors", [P0], primary=[CONNECTION_ERROR, CONNECTION_ERROR],
         config={"mask_errors": True})
scenario("two_invalid_responses_keep_raw_in_meta", [P0], primary=[invalid(3000), invalid(3000)],
         config={"mask_errors": True})
scenario("http_error_is_not_retried", [P0], primary=[http_error(500)], config={"mask_errors": True})
scenario("done_on_a_changed_page_is_asked_again", [P0, P1], primary=[done(), done()],
         fresh=[True, False, True, True])
scenario("action_budget_exhausted", [P0, P1, P2], primary=[click()] * 3, config={"max_steps": 2})
scenario("decision_budget_exhausted", [P0], primary=[done()] * 4, fresh=[True, False] * 4 + [True],
         config={"max_steps": 2})
scenario("browser_error_is_not_retried", [P0], primary=[click()],
         acts=[{"error": "runtime", "message": "CDP call timed out"}])
scenario("select_unconfirmed_is_not_retried", [P0], primary=[click()],
         acts=[{"error": "runtime", "message": "Dropdown execution was not confirmed; inspect before retrying"}])
scenario("gate_fires_on_low_confidence_done", [P0, P1],
         primary=[done(op={"DONE": 0.6, "CLICK": 0.3, "BLOCKED": 0.05, "WAIT": 0.05},
                       heads={"click_target": {"choice": "3", "probs": {"2": 0.1, "3": 0.9}}}), done()],
         config={"done_min_conf": 0.72})
scenario("gate_blocked_falls_back_to_a_control", [P0],
         primary=[blocked(op={"BLOCKED": 0.5, "WAIT": 0.4, "DONE": 0.1}), done()],
         config={"blocked_min_conf": 0.72})
scenario("gate_does_not_fire_below_the_floor", [P0],
         primary=[done(op={"DONE": 0.88, "CLICK": 0.12},
                       heads={"click_target": {"choice": "3", "probs": {"2": 0.1, "3": 0.9}}})],
         config={"done_min_conf": 0.9})
scenario("gate_does_not_fire_for_confident_done", [P0], primary=[done()], config={"done_min_conf": 0.72})
scenario("gate_skips_an_invalid_fallback_head", [P0],
         primary=[done(op={"DONE": 0.6, "CLICK": 0.24, "WAIT": 0.16},
                       heads={"click_target": {"raw": {"choice": "9", "confidence": 1.0,
                                                       "probabilities": {"9": 1.0}}}}), done()],
         config={"done_min_conf": 0.72})
scenario("gate_tie_follows_answer_order_wait_first", [P0],
         primary=[done(op={"DONE": 0.5, "WAIT": 0.25, "CLICK": 0.25}, order=["DONE", "WAIT", "CLICK"],
                       heads={"click_target": {"choice": "3", "probs": {"2": 0.1, "3": 0.9}}}), done()],
         config={"done_min_conf": 0.9})
scenario("gate_tie_follows_answer_order_click_first", [P0, P1],
         primary=[done(op={"DONE": 0.5, "WAIT": 0.25, "CLICK": 0.25}, order=["DONE", "CLICK", "WAIT"],
                       heads={"click_target": {"choice": "3", "probs": {"2": 0.1, "3": 0.9}}}), done()],
         config={"done_min_conf": 0.9})
SEARCH_HEAD = {"click_target": {"choice": "4", "probs": {"1": 0.1, "2": 0.2, "3": 0.05, "4": 0.65}}}
scenario("loop_guard_repeat_without_change", [M[0], M[0], M[0], M[1]],
         primary=[click("4", heads=SEARCH_HEAD)] * 3 + [done()], config={"loop_guard": True})
scenario("loop_guard_breaks_an_a_b_cycle", [M[0], M[1], M[2], M[3], M[4]],
         primary=[click("1"), click("2"), click("1"),
                  click("2", heads={"click_target": {"choice": "2",
                                                     "probs": {"1": 0.3, "2": 0.4, "3": 0.2, "4": 0.1}}}),
                  done()],
         config={"loop_guard": True})
scenario("loop_guard_repeated_refusal", [M[0], M[0], M[0], M[1]],
         primary=[click("1", heads={"click_target": {"choice": "1",
                                                     "probs": {"1": 0.7, "2": 0.1, "3": 0.15, "4": 0.05}}})] * 3
         + [done()],
         acts=["refused", "refused", None], config={"loop_guard": True})
scenario("loop_guard_leaves_progress_alone", [M[0], M[1], M[2], M[3], M[4]],
         primary=[click("1"), click("3"), click("1"), click("2"), done()], config={"loop_guard": True})
scenario("cascade_veto_cache_hit_then_cleared", [P0, P0, P1, P0],
         primary=[done(), done(), click(), done()], verifier=[click(), done()], config={"verifier": True})
scenario("cascade_target_confidence_escalates", [M[0], M[1]],
         primary=[click("1", heads={"click_target": {"choice": "1",
                                                     "probs": {"1": 0.4, "2": 0.3, "3": 0.2, "4": 0.1}}}), done()],
         verifier=[click("3"), done()], config={"verifier": True})
scenario("expansion_wait_until_options_appear",
         [combo("false"), combo("false"), combo("true"), combo("false"), combo("true", 2)],
         primary=[click("4"), done()])
scenario("expansion_wait_stops_at_the_limit", [combo("false")], primary=[click("4"), done()])
scenario("click_without_collapsed_state_does_not_wait", [P0, P1], primary=[click(), done()])
scenario("empty_page_is_reobserved", [P0, EMPTY, EMPTY, P1], primary=[click(), done()])
scenario("persistently_empty_page_proceeds", [P0, EMPTY], primary=[click(), done()])
scenario("blank_first_snapshot_of_a_new_page_is_reobserved", [P0, EMPTY_OTHER, EMPTY_OTHER, LOADED_OTHER],
         primary=[click(), done()])


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    for stale in OUT.glob("*.json"):
        stale.unlink()
    for sc in SCENARIOS:
        result = run(sc)
        (OUT / f"{sc['name']}.json").write_text(json.dumps(result, indent=1, ensure_ascii=False) + "\n")
        expected = result["expected"]
        print(f"{sc['name']:55} {expected['status']:10} steps={expected['steps']} history={len(expected['history'])} "
              f"error={expected['error']!r} reason={expected['blocked_reason']!r}")
    print(len(SCENARIOS), "scenarios written to", OUT)


if __name__ == "__main__":
    main()
