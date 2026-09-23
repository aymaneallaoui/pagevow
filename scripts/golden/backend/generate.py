"""Write golden fixtures for internal/backend from the Python reference.

Run: cd ~/jev-ultrafast && uv run python ~/pagevow/scripts/golden/backend/generate.py
No network, no browser: model.post_json is replaced by a stub that records requests and returns canned answers.
"""

import copy
import json
import os
from pathlib import Path

from jev_ultrafast import model

OUT = Path(__file__).resolve().parents[3] / "internal" / "backend" / "testdata"
PRIMARY = "http://primary"
VERIFIER = "http://verifier"
PRIMARY_KEY = "test-key"
VERIFIER_KEY = "verifier-key"
TARGET_OPERATIONS = ("CLICK", "TYPE_TEXT", "SELECT", "PRESS_ENTER")


def action(id, kind, label, node=None, role=None, value=None, **extra):
    item = {"id": id, "kind": kind, "label": label}
    if node is not None:
        item["node"] = node
    if role is not None:
        item["role"] = role
    if value is not None:
        item["value"] = value
    item.update(extra)
    return item


def state(url, title, text, actions):
    return {"url": url, "title": title, "text": text, "scroll": {"y": 0, "height": 1200}, "actions": actions}


def probabilities(ids, selected, top):
    others = [i for i in ids if i != selected]
    if not others:
        return {selected: 1.0}
    rest = round((1 - top) / len(others), 6)
    result = {i: rest for i in ids}
    result[selected] = top
    return result


def head(ids, selected, top, confidence=None):
    return {
        "choice": selected,
        "confidence": top if confidence is None else confidence,
        "probabilities": probabilities(ids, selected, top),
    }


def reply(body, operation, target=None, top=0.7, target_top=0.8, model_name="jev-test", break_unused=False):
    operations = list(body["questions"]["operation"]["criteria"])
    answers = {"operation": head(operations, operation, top)}
    for name, question in body["questions"].items():
        if name == "operation":
            continue
        ids = list(question["criteria"])
        owner = name[: -len("_target")].upper()
        if owner == operation:
            answers[name] = head(ids, target or ids[0], target_top)
        elif break_unused:
            answers[name] = {"choice": "nope"}
        else:
            answers[name] = head(ids, ids[0], 0.6)
    return {"model": model_name, "answers": answers, "usage": {"input_tokens": 120, "output_tokens": 4}}


class Stub:
    def __init__(self, primary, verifier=None):
        self.replies = {"primary": primary, "verifier": verifier}
        self.calls = []
        self.responses = {}

    def __call__(self, url, key, body):
        kind = "primary" if url.startswith(PRIMARY) else "verifier"
        assert url == (PRIMARY if kind == "primary" else VERIFIER) + "/v1/systemone", url
        assert key == (PRIMARY_KEY if kind == "primary" else VERIFIER_KEY), key
        self.calls.append(kind)
        reply_ = self.replies[kind]
        if isinstance(reply_, Exception):
            self.responses[kind] = {"__error__": "connection"}
            raise reply_
        result = reply_(body) if callable(reply_) else reply_
        self.responses[kind] = result
        return result


class FakeTrace:
    def __init__(self, steps):
        self.steps = steps

    def step(self, *args, **kwargs):
        pass


def set_env(verifier=False, target_conf=None, model_name=None):
    for name in ("JEV_VERIFIER_BASE_URL", "JEV_VERIFIER_API_KEY", "JEV_CASCADE_TARGET_CONF", "TYPESAFE_MODEL", "JEV_VETO_CACHE"):
        os.environ.pop(name, None)
    os.environ["TYPESAFE_API_KEY"] = PRIMARY_KEY
    os.environ["TYPESAFE_BASE_URL"] = PRIMARY
    if verifier:
        os.environ["JEV_VERIFIER_BASE_URL"] = VERIFIER + "/"
        os.environ["JEV_VERIFIER_API_KEY"] = VERIFIER_KEY
    if target_conf is not None:
        os.environ["JEV_CASCADE_TARGET_CONF"] = str(target_conf)
    if model_name:
        os.environ["TYPESAFE_MODEL"] = model_name


def decision_fields(decision):
    keys = (
        "choice",
        "operation",
        "target",
        "confidence",
        "probabilities",
        "target_ids",
        "operation_probabilities",
        "target_probabilities",
        "target_confidence",
        "raw_answers",
        "model",
        "usage",
    )
    return copy.deepcopy({key: decision[key] for key in keys})


def normalize(value):
    if isinstance(value, dict):
        return {
            key: ("<error>" if key == "error" else normalize(item))
            for key, item in value.items()
            if key != "latency_ms"
        }
    if isinstance(value, list):
        return [normalize(item) for item in value]
    return value


def write(name, payload):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / f"{name}.json").write_text(json.dumps(payload, indent=2, sort_keys=True, ensure_ascii=False) + "\n")


LONG_LABEL = "Subscribe to the quarterly newsletter about industrial design, typography and print " * 7


def history(count):
    entries = []
    for i in range(1, count + 1):
        entries.append(
            {
                "step": i,
                "action": f"Action {i}",
                "kind": "click" if i % 3 else "fill",
                "choice": f"e{i}",
                "probability": 0.9,
                "confidence": 0.9,
                "latency_ms": 100 + i,
                "text": f"value {i}" if i % 3 == 0 else None,
                "operation": "CLICK",
                "target": "1",
                "page_changed": None if i == count else bool(i % 2),
                "url": "https://example.test/",
            }
        )
    return entries


def choose_cases():
    cases = []

    cases.append(
        dict(
            name="clicks_only",
            goal="Open the pricing page",
            state=state(
                "https://example.test/",
                "Home",
                "Welcome\nPricing\nDocs",
                [
                    action("e1", "click", "Pricing", 10, "link", ""),
                    action("e2", "click", "Docs", 20, "link", ""),
                    action("e3", "click", "Sign in", 30, "button", ""),
                    action("wait", "wait", "Wait for the page to update"),
                ],
            ),
            history=[],
            operation="CLICK",
            target="1",
        )
    )

    form = state(
        "https://example.test/contact",
        "Contact",
        "Name\nEmail\nSend",
        [
            action("e1", "fill", "Name", 1, "textbox", ""),
            action("e2", "click", "Open Name", 1, "textbox", ""),
            action("e3", "fill", "Email", 2, "textbox", "ada@example.com"),
            action("e4", "click", "Open Email", 2, "textbox", "ada@example.com"),
            action("e5", "enter", "Press Enter in Email", 2, "textbox", "ada@example.com"),
            action("e6", "click", "Send", 3, "button", ""),
        ],
    )
    cases.append(
        dict(
            name="form_filled_field_enter",
            goal="Enter name Ada Lovelace and email ada@example.com, then send",
            state=form,
            history=history(2),
            operation="PRESS_ENTER",
            target="2",
        )
    )

    cases.append(
        dict(
            name="empty_value_field_type_text",
            goal="Enter the name Ada Lovelace. Do not send.",
            state=state(
                "https://example.test/contact",
                "Contact",
                "Name",
                [
                    action("e1", "fill", "Name", 1, "textbox", ""),
                    action("e2", "click", "Open Name", 1, "textbox", ""),
                ],
            ),
            history=[],
            operation="TYPE_TEXT",
            target="1",
        )
    )

    cases.append(
        dict(
            name="select_with_options",
            goal="Choose Germany as the country and Blue as the colour",
            state=state(
                "https://example.test/settings",
                "Settings",
                "Country\nColour",
                [
                    action("e1", "select", "Country → Germany", 1, "combobox", "de", current_value="France"),
                    action("e2", "select", "Country → Italy", 1, "combobox", "it", current_value="France"),
                    action("e3", "select", "Country → Spain", 1, "combobox", "es", current_value="France"),
                    action("e4", "select", "Colour → Blue", 2, "combobox", "blue", current_value=""),
                    action("e5", "click", "Save", 3, "button", ""),
                ],
            ),
            history=history(1),
            operation="SELECT",
            target="1:2",
        )
    )

    cases.append(
        dict(
            name="checkboxes",
            goal="Tick newsletter and untick terms",
            state=state(
                "https://example.test/prefs",
                "Preferences",
                "Newsletter\nTerms\nSave",
                [
                    action("e1", "click", "Newsletter", 1, "checkbox", "on", checked="false"),
                    action("e2", "click", "Terms", 2, "checkbox", "on", checked="true"),
                    action("e3", "click", "Notifications", 3, "switch", "", checked="false"),
                    action("e4", "click", "Save", 4, "button", ""),
                ],
            ),
            history=history(3),
            operation="CLICK",
            target="1",
        )
    )

    cases.append(
        dict(
            name="scroll_controls",
            goal="Find the footer link named Careers",
            state=state(
                "https://example.test/long",
                "Long page",
                "Section 1\nSection 2",
                [
                    action("e1", "click", "Section 1", 1, "link", ""),
                    action("scroll_down", "scroll", "Scroll down", delta=560),
                    action("scroll_up", "scroll", "Scroll up", delta=-560),
                    action("wait", "wait", "Wait for the page to update"),
                ],
            ),
            history=history(2),
            operation="SCROLL_DOWN",
            target=None,
        )
    )

    cases.append(
        dict(
            name="history_twelve_actions",
            goal="Finish the checkout",
            state=state(
                "https://example.test/cart",
                "Cart",
                "Cart\nCheckout",
                [action("e1", "click", "Checkout", 1, "button", ""), action("e2", "click", "Continue shopping", 2, "link", "")],
            ),
            history=history(12),
            operation="CLICK",
            target="1",
        )
    )

    cases.append(
        dict(
            name="long_label_and_unicode",
            goal="Subscribe: “quoted” goal with <tags> & ampersands → arrows",
            state=state(
                "https://example.test/café?q=a&b=<c>",
                "Café 日本語",
                "Café ‘menu’\n<b>bold</b> & more",
                [
                    action("e1", "click", LONG_LABEL, 1, "button", ""),
                    action("e2", "click", "Menu → Open → Deep", 2, "menuitem", ""),
                    action("e3", "click", "Café ‘Spécial’", 3, "link", ""),
                ],
            ),
            history=[],
            operation="CLICK",
            target="2",
        )
    )

    cases.append(
        dict(
            name="expanded_and_selected_states",
            goal="Open the Settings tab and expand the menu",
            state=state(
                "https://example.test/app",
                "App",
                "Overview\nSettings\nMenu",
                [
                    action("e1", "click", "Overview", 1, "tab", "", selected="true"),
                    action("e2", "click", "Settings", 2, "tab", "", selected="false"),
                    action("e3", "click", "Menu", 3, "button", "", expanded="false"),
                    action("e4", "fill", "Search", 4, "combobox", "", expanded="true"),
                    action("e5", "click", "Open Search", 4, "combobox", "", expanded="true"),
                ],
            ),
            history=history(1),
            operation="CLICK",
            target="2",
        )
    )

    cases.append(
        dict(
            name="done_chosen",
            goal="Enter the name and stop",
            state=form,
            history=history(4),
            operation="DONE",
            target=None,
        )
    )

    cases.append(
        dict(
            name="blocked_chosen",
            goal="Log in with the stored password",
            state=state("https://example.test/login", "Login", "Locked", [action("wait", "wait", "Wait for the page to update")]),
            history=history(5),
            operation="BLOCKED",
            target=None,
        )
    )

    cases.append(
        dict(
            name="only_controls_wait",
            goal="Wait for the results",
            state=state("https://example.test/loading", "Loading", "Loading", [action("wait", "wait", "Wait for the page to update")]),
            history=[],
            operation="WAIT",
            target=None,
        )
    )

    cases.append(
        dict(
            name="custom_model_unused_head_invalid",
            goal="Open the pricing page",
            model_name="my-model",
            break_unused=True,
            state=state(
                "https://example.test/",
                "Home",
                "Pricing",
                [action("e1", "click", "Pricing", 10, "link", ""), action("e2", "fill", "Search", 11, "textbox", "")],
            ),
            history=[],
            operation="CLICK",
            target="1",
        )
    )
    return cases


def run_choose(case):
    set_env(model_name=case.get("model_name"))
    stub = Stub(
        lambda body: reply(
            body, case["operation"], case["target"], model_name="jev-test", break_unused=case.get("break_unused", False)
        )
    )
    model.post_json = stub
    decision = model.choose(copy.deepcopy(case["state"]), case["goal"], copy.deepcopy(case["history"]))
    assert stub.calls == ["primary"]
    assert "cascade" not in decision
    write(
        f"choose_{case['name']}",
        {
            "name": case["name"],
            "goal": case["goal"],
            "model_option": case.get("model_name", ""),
            "state": case["state"],
            "history": case["history"],
            "response": copy.deepcopy(stub.responses["primary"]),
            "expected": {"request": decision["request"], "decision": decision_fields(decision)},
        },
    )


def cascade_state():
    return state(
        "https://example.test/",
        "Search",
        "Search",
        [
            action("e1", "fill", "Search", 10, "textbox", ""),
            action("e2", "click", "Open Search", 10, "textbox", ""),
            action("e3", "click", "Go", 20, "button", ""),
            action("e4", "click", "More", 30, "button", ""),
            action("wait", "wait", "Wait for the page to update"),
        ],
    )


GOAL = "Find a book"


def cascade_reply(operation, target_top=1.0, model_name="test"):
    return lambda body: reply(body, operation, "1", top=1.0, target_top=target_top, model_name=model_name)


def bad_verifier(body):
    result = reply(body, "CLICK", "1", top=0.9, target_top=0.9, model_name="large")
    result["answers"]["operation"]["probabilities"]["CLICK"] = 0.2
    return result


def run_scenario(name, steps, verifier=True, veto=False, target_conf=None):
    payload = {
        "name": name,
        "verifier": verifier,
        "veto_cache": veto,
        "target_confidence": target_conf if target_conf is not None else 0,
        "steps": [],
    }
    cache = {} if veto else None
    for number, spec in enumerate(steps):
        set_env(verifier=verifier, target_conf=target_conf)
        if spec.get("mutate_click_choice"):
            entry = next(iter(cache.values()))
            entry["result"]["answers"]["click_target"]["choice"] = spec["mutate_click_choice"]
        page = spec.get("state") or cascade_state()
        stub = Stub(spec["primary"], spec.get("verifier"))
        model.post_json = stub
        trace = FakeTrace(spec["trace_steps"]) if "trace_steps" in spec else None
        decision = model.choose(copy.deepcopy(page), GOAL, [], trace, veto_cache=cache)
        step = {
            "state": page,
            "mutate_click_choice": spec.get("mutate_click_choice", ""),
            "trace_step": spec["trace_steps"] + 1 if "trace_steps" in spec else 0,
            "responses": copy.deepcopy(stub.responses),
            "expected": {
                "calls": stub.calls,
                "decision": decision_fields(decision),
                "cascade": normalize(decision.get("cascade")),
                "cache_len": len(cache) if cache is not None else 0,
            },
        }
        if spec.get("forget_after"):
            cache.pop(model.veto_key(decision["request"]), None)
            step["forget_after"] = True
            step["expected"]["cache_len_after_forget"] = len(cache)
        payload["steps"].append(step)
    write(("veto_" if veto else "cascade_") + name, payload)


def scenarios():
    run_scenario("off_without_verifier", [{"primary": cascade_reply("DONE")}], verifier=False)
    run_scenario(
        "done_override", [{"primary": cascade_reply("DONE", model_name="small"), "verifier": cascade_reply("CLICK", model_name="large")}]
    )
    run_scenario("blocked_override", [{"primary": cascade_reply("BLOCKED"), "verifier": cascade_reply("CLICK")}])
    run_scenario("done_confirmed", [{"primary": cascade_reply("DONE"), "verifier": cascade_reply("DONE", model_name="large")}])
    run_scenario(
        "low_target_confidence", [{"primary": cascade_reply("CLICK", 0.4), "verifier": cascade_reply("CLICK", 0.9)}]
    )
    run_scenario("target_confidence_met", [{"primary": cascade_reply("CLICK", 0.6), "verifier": cascade_reply("CLICK", 0.9)}])
    run_scenario(
        "custom_target_confidence",
        [{"primary": cascade_reply("CLICK", 0.6), "verifier": cascade_reply("CLICK", 0.9)}],
        target_conf=0.7,
    )
    run_scenario(
        "verifier_connection_failure",
        [{"primary": cascade_reply("DONE"), "verifier": model.ModelConnectionError("Model connection failed; no action executed.")}],
    )
    run_scenario("verifier_invalid_response", [{"primary": cascade_reply("DONE"), "verifier": bad_verifier}])

    run_scenario(
        "store_and_reuse",
        [
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK", model_name="large")},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK", model_name="large")},
        ],
        veto=True,
    )
    run_scenario(
        "blocked_store_and_reuse_with_trace_step",
        [
            {"primary": cascade_reply("BLOCKED"), "verifier": cascade_reply("CLICK"), "trace_steps": 4},
            {"primary": cascade_reply("BLOCKED"), "verifier": cascade_reply("CLICK"), "trace_steps": 5},
        ],
        veto=True,
    )
    run_scenario(
        "no_store_on_agreement",
        [
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("DONE")},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("DONE")},
        ],
        veto=True,
    )
    run_scenario(
        "no_store_for_target_confidence",
        [{"primary": cascade_reply("CLICK", 0.4), "verifier": cascade_reply("DONE")}],
        veto=True,
    )
    run_scenario(
        "invalidated_entry",
        [
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK")},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK"), "mutate_click_choice": "99"},
        ],
        veto=True,
    )
    other_url = cascade_state()
    other_url["url"] = "https://example.test/next"
    other_label = cascade_state()
    other_label["actions"][2]["label"] = "Proceed"
    run_scenario(
        "different_key",
        [
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK")},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK"), "state": other_url},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK"), "state": other_label},
        ],
        veto=True,
    )
    run_scenario(
        "forget_after_page_change",
        [
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK"), "forget_after": True},
            {"primary": cascade_reply("DONE"), "verifier": cascade_reply("CLICK")},
        ],
        veto=True,
    )


def main():
    for case in choose_cases():
        run_choose(case)
    scenarios()


main()
