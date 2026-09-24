"""Writes texthelper fixtures from the Python reference. Run from ~/jev-ultrafast:
uv run python /home/aymane/pagevow/scripts/golden/texthelper/generate.py"""

import json
import os
from pathlib import Path

from jev_ultrafast import model

OUT = Path(__file__).resolve().parents[3] / "internal" / "texthelper" / "testdata"
ENV_KEYS = ("TEXT_MODEL_BASE_URL", "TEXT_MODEL", "TEXT_MODEL_REASONING", "TEXT_MODEL_API_KEY")

LONG_TEXT = "".join(f"line {i} of the page with some words\n" for i in range(400))
HISTORY = [
    {"step": i, "action": f"Field {i}", "kind": "fill", "text": None if i % 3 == 0 else f"value {i}", "page_changed": False}
    for i in range(1, 10)
]

CASES = [
    {
        "name": "deepseek_default",
        "env": {},
        "goal": "On the contact page, enter Ada Lovelace as the name.",
        "action": {"id": "a1", "kind": "fill", "label": "Name", "role": "textbox", "value": ""},
        "page": {"title": "Contact - Shelf", "text": "Shelf\nContact us\nName\nEmail\nSend"},
        "history": [],
    },
    {
        "name": "deepseek_without_path_takes_default_reasoning",
        "env": {"TEXT_MODEL_BASE_URL": "https://api.deepseek.com/", "TEXT_MODEL": "deepseek-reasoner"},
        "goal": "Enter the email.",
        "action": {"id": "a2", "kind": "fill", "label": "Email", "role": "textbox", "value": "old@example.com"},
        "page": {"title": "Form", "text": "Email"},
        "history": [{"action": "Name", "text": "Ada Lovelace"}],
    },
    {
        "name": "custom_base_default_reasoning",
        "env": {"TEXT_MODEL_BASE_URL": "http://127.0.0.1:8011/v1/", "TEXT_MODEL": "qwen3-4b"},
        "goal": "Search for 'wireless mouse' & \"ergonomic\" <keyboards>",
        "action": {"id": "a3", "kind": "fill", "label": "Search", "role": "searchbox", "value": None},
        "page": {"title": "Shop", "text": "Search products"},
        "history": [],
    },
    {
        "name": "reasoning_none",
        "env": {"TEXT_MODEL_BASE_URL": "http://127.0.0.1:8011/v1", "TEXT_MODEL_REASONING": "none"},
        "goal": "Enter a message.",
        "action": {"id": "a4", "kind": "fill", "label": "Message", "role": "textbox", "value": ""},
        "page": {"title": "Contact", "text": "Message"},
        "history": [],
    },
    {
        "name": "reasoning_none_overrides_deepseek",
        "env": {"TEXT_MODEL_REASONING": "none"},
        "goal": "Enter a message.",
        "action": {"id": "a5", "kind": "fill", "label": "Message", "role": "textbox", "value": ""},
        "page": {"title": "Contact", "text": "Message"},
        "history": [],
    },
    {
        "name": "unicode_and_control_characters",
        "env": {"TEXT_MODEL": "gpt-4o-mini", "TEXT_MODEL_BASE_URL": "https://example.test/v1"},
        "goal": "Entrez le prénom \"Zoë\" et l'emoji 😀 - 日本語\ttab\nnewline   sep \x7f del \x01",
        "action": {"id": "a6", "kind": "fill", "label": "Prénom → nom", "role": "textbox", "value": "é"},
        "page": {"title": "Café ☕", "text": "Ligne 1\r\nLigne 2 \\ back\\slash"},
        "history": [{"action": "Ville → Zürich", "text": "Zürich"}],
    },
    {
        "name": "long_page_and_long_history",
        "env": {"TEXT_MODEL_BASE_URL": "https://example.test/v1"},
        "goal": "Fill the form.",
        "action": {"id": "a7", "kind": "fill", "label": "Comment", "role": "textbox", "value": ""},
        "page": {"title": "Long", "text": LONG_TEXT},
        "history": HISTORY,
    },
    {
        "name": "page_text_cut_inside_astral_character",
        "env": {"TEXT_MODEL_BASE_URL": "https://example.test/v1"},
        "goal": "Fill the form.",
        "action": {"id": "a8", "kind": "fill", "label": "Comment"},
        "page": {"title": "Emoji", "text": "x" * 5999 + "😀😀 tail"},
        "history": [],
    },
]

REPLIES = [
    ("plain_json", '{"text": "Zurich"}'),
    ("plain_json_compact", '{"text":"Zurich"}'),
    ("fenced_json", '```json\n{"text": "Rust"}\n```'),
    ("json_with_trailing_text", '{"text": "Zurich"} Hope this helps!'),
    ("json_with_leading_text", 'Sure: {"text": "a {b} c"} done'),
    ("two_keys", '{"text": "x", "extra": 1}'),
    ("empty_text", '{"text": ""}'),
    ("whitespace_text", '{"text": "  \\t\\n"}'),
    ("separator_text", '{"text": "\\u001c\\u001d"}'),
    ("null_text", '{"text": null}'),
    ("number_text", '{"text": 123}'),
    ("bool_text", '{"text": true}'),
    ("text_2000_chars", json.dumps({"text": "a" * 2000})),
    ("text_2001_chars", json.dumps({"text": "a" * 2001})),
    ("text_2000_astral_chars", json.dumps({"text": "😀" * 2000})),
    ("text_2001_astral_chars", json.dumps({"text": "😀" * 2001})),
    ("no_json", "Thinking: Zurich"),
    ("array_of_strings", '["text"]'),
    ("array_of_objects", '[{"text": "inner"}]'),
    ("nested_object", '{"a": {"text": "x"}}'),
    ("wrong_key", '{"value": "x"}'),
    ("duplicate_key", '{"text": "first", "text": "second"}'),
    ("unicode_text", '{"text": "Zoë \\u2603 😀"}'),
    ("unterminated_json", '{"text": "abc'),
    ("brace_only", "{"),
    ("long_garbage", "x" * 500),
    ("apostrophe_in_reply", "it's not json"),
    ("both_quote_kinds", "it's \"not\" json"),
    ("control_in_reply", "tab\there\nnewline \x01 é ☃"),
]
RESULT_SHAPES = [
    ("content_null", {"choices": [{"message": {"content": None}}]}),
    ("content_number", {"choices": [{"message": {"content": 5}}]}),
    ("no_choices", {"choices": []}),
    ("missing_choices", {"id": "x"}),
    ("no_message", {"choices": [{}]}),
    ("top_level_array", []),
    ("usage_absent", {"choices": [{"message": {"content": '{"text": "ok"}'}}]}),
    ("usage_present", {"choices": [{"message": {"content": '{"text": "ok"}'}}], "usage": {"input_tokens": 3}}),
    ("usage_null", {"choices": [{"message": {"content": '{"text": "ok"}'}}], "usage": None}),
]


def apply_env(env):
    for key in ENV_KEYS:
        os.environ.pop(key, None)
    os.environ["TEXT_MODEL_API_KEY"] = "fixture-key"
    os.environ.update(env)


def request_fixture(case):
    apply_env(case["env"])
    captured = {}

    def fake_post_within(budget, url, key, body):
        captured.update(budget=budget, url=url, key=key, body=body)
        return {"choices": [{"message": {"content": '{"text": "x"}'}}]}

    original = model.post_within
    model.post_within = fake_post_within
    try:
        context = model.field_context(case["goal"], case["action"], case["page"], case["history"])
        model.field_text(context)
    finally:
        model.post_within = original
    body = captured["body"]
    return {
        "name": case["name"],
        "config": {
            "base_url": case["env"].get("TEXT_MODEL_BASE_URL", "https://api.deepseek.com/v1"),
            "model": case["env"].get("TEXT_MODEL", "deepseek-chat"),
            "reasoning": case["env"].get("TEXT_MODEL_REASONING", ""),
        },
        "input": {
            "goal": case["goal"],
            "action": case["action"],
            "page": case["page"],
            "history": case["history"],
        },
        "expected": {"url": captured["url"], "body": body, "user_content": body["messages"][1]["content"]},
    }


def outcome(result):
    apply_env({})
    original = model.post_json
    model.post_json = lambda *args, **kwargs: result
    try:
        value, helper = model.field_text({"goal": "g"})
        return {"value": value, "usage": helper["usage"]}
    except Exception as error:
        return {"error": type(error).__name__, "message": str(error)}
    finally:
        model.post_json = original


def reply_fixtures():
    fixtures = []
    for name, content in REPLIES:
        result = {"choices": [{"message": {"content": content}}]}
        fixtures.append({"name": name, "result": result, "content_is_string": True, "outcome": outcome(result)})
    for name, result in RESULT_SHAPES:
        fixtures.append(
            {
                "name": name,
                "result": result,
                "content_is_string": name.startswith("usage_"),
                "outcome": outcome(result),
            }
        )
    return fixtures


def main():
    OUT.mkdir(parents=True, exist_ok=True)
    requests = [request_fixture(case) for case in CASES]
    replies = reply_fixtures()
    (OUT / "requests.json").write_text(json.dumps(requests, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (OUT / "replies.json").write_text(json.dumps(replies, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    (OUT / "system_prompt.txt").write_text(model.TEXT_VALUE, encoding="utf-8")
    print(f"{len(requests)} request fixtures, {len(replies)} reply fixtures in {OUT}")


if __name__ == "__main__":
    main()
