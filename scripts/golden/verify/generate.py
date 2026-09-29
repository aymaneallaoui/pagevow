"""Writes golden fixtures for internal/verify from the Python reference in jev_ultrafast/verifiers.py.

Run: cd ~/jev-ultrafast && uv run python ~/pagevow/scripts/golden/verify/generate.py
"""

import base64
import importlib.util
import json
import os
import re
import unicodedata
from datetime import date, timedelta
from pathlib import Path
from urllib.parse import parse_qs, unquote_plus, urljoin, urlparse

import yaml

JEV = Path(os.environ.get("JEV_ROOT", Path.cwd()))
OUT = Path(__file__).resolve().parents[3] / "internal" / "verify" / "testdata"
DEMO = Path(os.environ.get("DEMO_TESTS", Path.home() / "browser-test-demo" / "browser-tests.yaml"))

spec = importlib.util.spec_from_file_location("verifiers", JEV / "jev_ultrafast" / "verifiers.py")
V = importlib.util.module_from_spec(spec)
spec.loader.exec_module(V)

TODAY = date(2026, 9, 28)
FUNCTIONS = {"page": V.page, "echo": V.echo, "flights": V.flights, "hn_story": V.hn_story}


def write(name, data):
    OUT.mkdir(parents=True, exist_ok=True)
    (OUT / name).write_text(json.dumps(data, ensure_ascii=False, indent=1) + "\n", encoding="utf-8")
    print(f"{name}: {len(data)}")


def state(url, text="", actions=(), guards=None):
    return {
        "url": url, "title": "", "w": 1280, "h": 800, "text": text,
        "scroll": {"y": 0, "height": 800}, "actions": list(actions), "guards": guards or {},
    }


def act(label, kind="click", **fields):
    return {"id": "e0", "kind": kind, "label": label, **fields}


def box(label, node, checked):
    return act(label, role="checkbox", node=node, checked=checked)


CASES = []


def add(name, verifier, final, args, initial=None):
    text = args if isinstance(args, str) else yaml.safe_dump(args, allow_unicode=True, sort_keys=False)
    arguments = yaml.safe_load(text) or {}
    for source, target in (("date", "day"), ("return_date", "return_day")):
        if source in arguments:
            arguments[target] = date.fromisoformat(arguments.pop(source))
    call = dict(arguments)
    if verifier == "hn_story":
        call["initial"] = initial
    entry = {"name": name, "verifier": verifier, "final": final, "initial": initial, "args_yaml": text}
    try:
        result = FUNCTIONS[verifier](final, **call)
    except Exception as error:
        entry.update(error=f"{type(error).__name__}: {error}", result=None, explain=None)
    else:
        record = {"passed": result["passed"], "checks": [[k, v] for k, v in result["checks"].items()]}
        for key in ("expected", "visible_flights"):
            if key in result:
                record[key] = result[key]
        entry.update(error=None, result=record, explain=V.explain(final, result, **arguments))
    CASES.append(entry)


def form_page(url="https://shop.example.com/contact.html?topic=Returns&q=film+camera%20x", **more):
    actions = [
        act("Name", "fill", role="textbox", node=1, value="Ada Lovelace"),
        act("Email:", "fill", role="textbox", node=2, value="ada@example.com"),
        act("Topic → Order", "select", role="combobox", node=3, value="order", current_value="Returns"),
        act("Topic → Billing", "select", role="combobox", node=3, value="billing", current_value="Returns"),
        act("Newsletter", role="checkbox", node=4, checked="true", value="on"),
        act("Gift wrap", role="checkbox", node=5, checked="false", value="on"),
        act("Message  →  Draft", "fill", role="textbox", node=6, value="Damaged cover"),
        act("Send", role="button", node=7, value=""),
    ]
    return state(url, more.pop("text", "Contact us\nRésumé  of Zürich"), actions)


def page_cases():
    p = form_page()
    add("page url matches decoded case-insensitively", "page", p, {"url": r"contact\.HTML\?topic=returns&q=film camera x"})
    add("page url mismatch", "page", p, {"url": r"/cart\.html"})
    add("page url list with one failure", "page", p, {"url": [r"contact\.html", r"[?&]topic=billing(&|$)"]})
    add("page url plus and percent 20 are spaces", "page", p, {"url": "q=film camera x$"})
    add("page url utf8 escapes decode", "page", state("https://en.wikipedia.org/wiki/G%C3%B6del%27s_theorem", "x"),
        {"url": "wiki/Gödel's_theorem$", "text": "x"})
    add("page url invalid utf8 escape becomes replacement", "page", state("https://x.test/a%E2%82b%FFc%C3%A9", ""),
        {"url": "a\ufffdb\ufffdcé$"})
    add("page url with quantifier braces", "page", state("https://x.test/2026-09-28", ""), {"url": r"/\d{4}-\d{2}-\d{2}$"})
    add("page url anchored with hash", "page", state("https://en.wikipedia.org/wiki/Linux#History", "History"),
        {"url": r"wiki/Linux#History$", "text": "History"})
    add("page url unicode case folding", "page", state("https://x.test/ÉTÉ", ""), {"url": "/été$"})
    add("page url percent in path is left when invalid", "page", state("https://x.test/100%/a%zz/b%4", ""),
        {"url": r"100%/a%zz/b%4$"})

    add("page text accents and case", "page", p, {"text": ["resume of zurich", "CONTACT US"]})
    add("page text missing", "page", p, {"text": ["Résumé", "Refund policy"]})
    add("page text empty needle", "page", p, {"text": [""]})
    add("page text nbsp and spaces", "page", state("https://x.test/", "Total:\u00a0\u00a0$12.50\n\tdue"), {"text": ["total: $12.50 due"]})
    add("page text fullwidth and ligature", "page", state("https://x.test/", "ＡＢＣ１２３ ﬁnal Ⅷ"), {"text": ["abc123 final viii"]})
    add("page text arabic diacritics", "page", state("https://x.test/", "مَرْحَبًا بِكُمْ"), {"text": ["مرحبا بكم"]})
    add("page text sharp s and sigma", "page", state("https://x.test/", "Straße ΟΔΟΣ"), {"text": ["STRASSE", "οδος", "οδοσ"]})
    add("page text control 1c is space", "page", state("https://x.test/", "a\x1cb\x1fc"), {"text": ["a b c"]})
    add("page text zero width space is kept", "page", state("https://x.test/", "a\u200bb"), {"text": ["ab", "a b"]})
    add("page text turkish dotted i", "page", state("https://x.test/", "İstanbul"), {"text": ["istanbul", "i̇stanbul"]})

    add("page field label variants", "page", p, {"fields": {"name": "ada lovelace", "  EMAIL  ": "Ada@Example.com", "Email:": "ada@example.com"}})
    add("page field arrow suffix and select current value", "page", p, {"fields": {"Topic": "Returns"}})
    add("page field message label with arrow", "page", p, {"fields": {"Message": "damaged   cover"}})
    add("page field value mismatch", "page", p, {"fields": {"Name": "Grace Hopper"}})
    add("page field label missing", "page", p, {"fields": {"Phone": "555"}})
    add("page field several actions share a label", "page", p, {"fields": {"Topic": "Order"}})
    add("page field checkbox true and false", "page", p, {"fields": {"Newsletter": True, "Gift wrap": False}})
    add("page field checkbox wrong state", "page", p, {"fields": {"Newsletter": False, "Gift wrap": True}})
    add("page field bool on a text field", "page", p, {"fields": {"Name": True}})
    add("page field bool on labels without state", "page", state("https://x.test/", "", [act("Agree", value="")]), {"fields": {"Agree": True}})
    add("page field aria checked value is not a checkbox state", "page",
        state("https://x.test/", "", [act("Agree", role="checkbox", node=1, checked="mixed")]), {"fields": {"Agree": True}})
    add("page field text on checkbox compares its value", "page", p, {"fields": {"Newsletter": "on"}})
    add("page field unicode expected", "page", state("https://x.test/", "", [act("Ville", "fill", value="Zürich")]),
        {"fields": {"ville": "ZURICH"}})
    add("page field falls back to value without current value", "page",
        state("https://x.test/", "", [act("City", "fill", value="Paris")]), {"fields": {"City": "paris"}})

    add("page field int expected", "page", state("https://x.test/", "", [act("Quantity", "fill", value="2")]), {"fields": {"Quantity": 2}})
    add("page field float expected uses python repr", "page", state("https://x.test/", "", [act("Price", "fill", value="1.5")]),
        {"fields": {"Price": 1.50}})
    add("page field float expected mismatch", "page", state("https://x.test/", "", [act("Price", "fill", value="1.50")]),
        "fields: {Price: 1.50}\n")
    add("page field yaml yes is a checkbox state", "page", p, "fields: {Newsletter: yes, Gift wrap: no}\n")
    add("page field null expected is the word None", "page", state("https://x.test/", "", [act("Note", "fill", value="none")]),
        "fields: {Note: ~}\n")
    add("page field sexagesimal int", "page", state("https://x.test/", "", [act("Time", "fill", value="750")]), "fields: {Time: 12:30}\n")
    add("page field hex and underscore ints", "page", state("https://x.test/", "", [act("A", "fill", value="31"), act("B", "fill", value="1000")]),
        "fields: {A: 0x1F, B: 1_000}\n")
    add("page field quoted number stays text", "page", state("https://x.test/", "", [act("Zip", "fill", value="01234")]),
        "fields: {Zip: '01234'}\n")
    add("page field octal-looking number", "page", state("https://x.test/", "", [act("Zip", "fill", value="668")]),
        "fields: {Zip: 0668}\n")

    boxes = state("https://the-internet.herokuapp.com/checkboxes", "", [
        box("checkbox", 1, "true"), box("checkbox", 2, "false"),
        act("Please select → Option 2", "select", role="combobox", node=3, current_value="Option 2", value="2"),
    ])
    add("page checked and values pass", "page", boxes, {"values": ["Option 2"], "checked": {"checkbox": 1}})
    add("page checked count mismatch", "page", boxes, {"checked": {"checkbox": 2}})
    add("page checked label missing counts zero", "page", boxes, {"checked": {"radio": 1}})
    add("page checked zero expected", "page", boxes, {"checked": {"radio": 0}})
    add("page checked dedupes actions by node", "page", state("https://x.test/", "", [
        box("Agree", 1, "true"), box("Agree", 1, "true"), box("Agree", 2, "true"), box("agree", 3, "false")]),
        {"checked": {"Agree": 2}})
    add("page value missing", "page", boxes, {"values": ["Option 1"]})
    add("page value numeric and float", "page", state("https://x.test/", "", [act("Age", "fill", value="42"), act("Ratio", "fill", value="0.5")]),
        "values: [42, 0.50]\n")
    add("page value unlabeled control accents", "page", state("https://x.test/", "", [act("", "fill", value="Zürich")]),
        {"values": ["zurich"]})
    add("page value from current value null-free", "page", state("https://x.test/", "", [act("x", "select", current_value="Blue", value="b")]),
        {"values": ["Blue", "b"]})
    add("page no arguments passes", "page", p, "{}\n")
    add("page every check kind together", "page", p, {
        "url": r"contact\.html", "text": ["Contact us"], "fields": {"Name": "Ada Lovelace", "Newsletter": True},
        "values": ["ada@example.com"], "checked": {"Newsletter": 1}})
    add("page duplicate check names collapse", "page", p, {"text": ["contact us", "contact us"], "url": [r"contact", r"contact"]})
    add("page field label with several colons", "page", state("https://x.test/", "", [act("Total::", "fill", value="5")]),
        {"fields": {"Total": 5}})
    add("page label with quote characters explain", "page", state("https://x.test/", "", [act("It's", "fill", value='say "hi"')]),
        {"fields": {"It's": "x"}})
    add("page non ascii label explain", "page", state("https://x.test/", "", [act("Prénom", "fill", value="Zoë\n")]),
        {"fields": {"Prénom": "Zoe"}, "text": ["Zoë\u200b"]})

    for name, final, args in (
        ("echo passes", state("https://httpbin.org/post", '"form": { "custname": "Test User", "size": "medium" }'),
         {"url": r"httpbin\.org/post$", "values": ['"custname": "Test User"', '"size": "medium"']}),
        ("echo missing value", state("https://httpbin.org/post", '"form": { "custname": "Test User" }'),
         {"url": r"httpbin\.org/post$", "values": ['"topping": "bacon"']}),
        ("echo wrong url", state("https://httpbin.org/forms/post", '"custname": "Test User"'),
         {"url": r"httpbin\.org/post$", "values": ['"custname": "Test User"']}),
        ("echo url list", state("https://httpbin.org/post", "ok"), {"url": [r"httpbin", r"/get$"], "values": "ok"}),
    ):
        add(name, "echo", final, args)
    subscribe = state("https://x.test/", "", [
        act("Subscribe", "fill", role="textbox", node=1, value="yes"), box("Newsletter", 2, "true"), act("Count", "fill", role="textbox", node=3, value="12"),
    ])
    add("page explicit str tag keeps yes a string", "page", subscribe, "fields: {Subscribe: !!str yes}\n")
    add("page plain yes is a boolean and fails on a text field", "page", subscribe, "fields: {Subscribe: yes}\n")
    add("page explicit bool tag", "page", subscribe, "fields: {Newsletter: !!bool yes}\n")
    add("page explicit int tag in values", "page", subscribe, "values: [!!int '12', !!str 12]\n")
    add("page explicit str tag on a number", "page", subscribe, "fields: {Count: !!str 12}\n")
    add("page explicit float tag", "page", subscribe, "fields: {Count: !!float 12}\n")


def hn_front():
    rows = [
        ("1.\t\n\tFirst story (a.com)", ["vote?id=11&how=up&goto=news", "https://a.com/post/", "from?site=a.com"]),
        ("2.\t\n\tAsk HN: Second", ["vote?id=22&how=up&goto=news", "item?id=22"]),
        ("3.\t\n\tRelative", ["vote?id=33&how=up&goto=news", "../docs/page.html?x=1#top", "hide?id=33&goto=news"]),
        ("4.\t\n\tProtocol relative", ["vote?id=44&how=up&goto=news", "//cdn.example.org/p/", "from?site=cdn.example.org"]),
        ("10.\t\n\tTen", ["vote?id=1010&how=up&goto=news", "https://ten.example.com/x"]),
        ("5.\t\n\tOnly vote", ["vote?id=55&how=up&goto=news"]),
        ("6.\t\n\tEmpty href first", ["", "https://six.example.com/"]),
        ("7.\t\n\tHost only", ["vote?id=77&goto=news", "https://seven.example.com"]),
    ]
    actions, guards, node = [], {}, 0
    for scope, hrefs in rows:
        for href in hrefs:
            node += 1
            actions.append(act(href or "link", node=node))
            guards[str(node)] = [node] + [None] * 11 + [href, scope]
    node += 1
    actions.append(act("More", node=node))
    guards[str(node)] = None
    node += 1
    actions.append(act("Search", "fill", node=node, value=""))
    guards[str(node)] = [node] + [None] * 11 + ["ignored", "1.\t"]
    return state("https://news.ycombinator.com/news", "", actions, guards)


def hn_cases():
    initial = hn_front()
    item = "https://news.ycombinator.com/item?id="
    for name, url, args in (
        ("hn rank 1 link", "https://a.com/post", {"rank": 1}),
        ("hn rank 1 link www and slash", "https://www.a.com/post/", {"rank": 1}),
        ("hn rank 1 other host", "https://b.com/post", {"rank": 1}),
        ("hn rank 1 upper case host and port", "https://A.COM:8080/post", {"rank": 1}),
        ("hn rank 1 query differs", "https://a.com/post?x=1", {"rank": 1}),
        ("hn rank 2 ask item", item + "22", {"rank": 2}),
        ("hn rank 2 item wrong id", item + "23", {"rank": 2}),
        ("hn rank 1 comments", item + "11", {"rank": 1, "comments": True}),
        ("hn rank 1 comments wrong thread", item + "22", {"rank": 1, "comments": True}),
        ("hn rank 3 relative join", "https://news.ycombinator.com/docs/page.html?x=1", {"rank": 3}),
        ("hn rank 3 relative join wrong", "https://news.ycombinator.com/docs/page.html", {"rank": 3}),
        ("hn rank 4 protocol relative", "https://cdn.example.org/p", {"rank": 4}),
        ("hn rank 5 no title", "https://x.test/", {"rank": 5}),
        ("hn rank 5 comments", item + "55", {"rank": 5, "comments": True}),
        ("hn rank 6 empty first href", "https://six.example.com/", {"rank": 6}),
        ("hn rank 7 host only", "https://seven.example.com/", {"rank": 7}),
        ("hn rank 10 not matched by rank 1", "https://ten.example.com/x", {"rank": 10}),
        ("hn rank 1 not matched by rank 10 prefix", "https://ten.example.com/x", {"rank": 1}),
        ("hn rank 9 missing", item + "99", {"rank": 9, "comments": True}),
        ("hn rank 9 missing link", "https://x.test", {"rank": 9}),
    ):
        add(name, "hn_story", state(url), args, initial=initial)
    add("hn no guards at all", "hn_story", state("https://a.com/"), {"rank": 1},
        initial=state("https://news.ycombinator.com/", "", [act("x", node=1)], {}))


def flights_page(tfs=None, text="departing 2026-10-12", url=None, adults=2, **values):
    fields = {
        "Change ticket type. Round trip": "Round trip", "Where from?": "Zürich", "Where to?": "New York, NY",
        "Departure": "Mon, Oct 12", "Return": "Mon, Oct 19", **values,
    }
    query = f"?tfs={tfs}" if tfs is not None else "?tfs=example"
    return {
        **state(url or f"https://www.google.com/travel/flights/search{query}", text),
        "actions": [
            *(act(label, "fill", value=value) for label, value in fields.items()),
            act(f"{adults} passengers, change number of passengers.", role="button"),
            act("Passenger assistance", role="button"),
            act("Nonstop flight on Monday, October 12. Select flight", value=""),
            act("Flight on Monday, October 12. Select flight", value=""),
        ],
    }


def tfs(day="2026-10-12", pad=False):
    raw = b"\x08\x1c\x10\x02\x1a\x1e\x12\n" + day.encode() + b"jbc\x02ZRHrbc\x02LON"
    encoded = base64.urlsafe_b64encode(raw).decode()
    return encoded if pad else encoded.rstrip("=")


def flights_cases():
    route = {"origin": "Zurich", "destination": "new york", "date": "2026-10-12", "one_way": False, "return_date": "2026-10-19"}
    add("flights round trip passes via tfs", "flights", flights_page(tfs=tfs()), {**route, "adults": 2})
    add("flights round trip passes via text", "flights", flights_page(), {**route, "adults": 2})
    add("flights padded tfs", "flights", flights_page(tfs=tfs(pad=True) , text=""), route)
    add("flights percent encoded padding", "flights", flights_page(tfs=tfs(pad=True).replace("=", "%3D"), text=""), route)
    add("flights wrong day in tfs and text", "flights", flights_page(tfs=tfs("2026-10-13"), text="departing 2026-10-13"), route)
    add("flights no year evidence", "flights", flights_page(text=""), route)
    add("flights garbage tfs falls back to text", "flights", flights_page(tfs="!!!"), route)
    add("flights bad padding tfs", "flights", flights_page(tfs="A", text=""), route)
    add("flights non ascii tfs", "flights", flights_page(tfs="ünï", text=""), route)
    add("flights blank tfs", "flights", flights_page(url="https://www.google.com/travel/flights/search?tfs=&x=1", text=""), route)
    add("flights first tfs wins", "flights", flights_page(url=f"https://www.google.com/travel/flights/search?tfs={tfs()}&tfs=other", text=""), route)
    add("flights wrong host", "flights", flights_page(url="https://www.bing.com/travel/flights/search?tfs=x"), route)
    add("flights wrong path", "flights", flights_page(url="https://www.google.com/travel/flights?tfs=x"), route)
    add("flights uppercase host and port", "flights", flights_page(url="https://WWW.Google.com:443/travel/flights/search?tfs=x"), route)
    add("flights path with params", "flights", flights_page(url="https://www.google.com/travel/flights/search;x=1?tfs=x"), route)
    add("flights origin accents", "flights", flights_page(**{"Where from?": "Zurich"}), {**route, "origin": "ZÜRICH"})
    add("flights origin mismatch", "flights", flights_page(**{"Where from?": "Geneva"}), route)
    add("flights origin empty", "flights", flights_page(**{"Where from?": ""}), route)
    add("flights destination missing", "flights", {**flights_page(), "actions": [a for a in flights_page()["actions"] if a["label"] != "Where to?"]}, route)
    add("flights wrong departure", "flights", flights_page(Departure="Tue, Oct 13"), route)
    add("flights wrong return", "flights", flights_page(Return="Mon, Oct 20"), route)
    add("flights ticket type mismatch", "flights", flights_page(**{"Change ticket type. Round trip": "One way"}), route)
    add("flights passengers mismatch", "flights", flights_page(adults=3), {**route, "adults": 2})
    add("flights passengers singular", "flights", flights_page(adults=1), {**route, "adults": 1})
    add("flights passengers none", "flights", flights_page(), route)
    add("flights results on another date", "flights", {**flights_page(), "actions": flights_page()["actions"] + [act("Nonstop flight on Tuesday, October 13. Select flight", value="")]}, route)
    add("flights no results", "flights", {**flights_page(), "actions": [a for a in flights_page()["actions"] if "Select flight" not in a["label"]]}, route)
    one_way = {"origin": "Zurich", "destination": "New York", "date": "2026-10-12", "one_way": True, "adults": 2}
    one_way_page = flights_page(**{"Change ticket type. One way": "One way"})
    add("flights one way passes", "flights", one_way_page, one_way)
    add("flights one way default", "flights", one_way_page, {k: v for k, v in one_way.items() if k != "one_way"})
    add("flights one way wrong ticket type", "flights", flights_page(), one_way)
    add("flights one way ignores return", "flights", one_way_page, {**one_way, "return_date": "2026-10-19"})
    add("flights round trip without return day", "flights", flights_page(), {"origin": "A", "destination": "B", "date": "2026-10-12", "one_way": False})
    add("flights duplicate labels last value wins", "flights", {**flights_page(), "actions": flights_page()["actions"] + [act("Departure", "fill", value="Sun, Oct 11")]}, route)
    add("flights label whitespace is stripped", "flights", {**flights_page(), "actions": [
        {**a, "label": "  " + a["label"] + "\u00a0"} if a["label"] == "Where to?" else a for a in flights_page()["actions"]]}, route)
    add("flights single digit day", "flights", flights_page(Departure="Sat, Nov 7", Return="Sat, Nov 14", text="departing 2026-11-07",
        **{}), {**route, "date": "2026-11-07", "return_date": "2026-11-14"})


def demo_cases():
    tests = yaml.safe_load(DEMO.read_text(encoding="utf-8"))
    shelf = "http://localhost:3000/"
    pages = {
        "nav-to-catalog": (
            state(shelf + "catalog.html", "Shelf\nCatalog\nShowing 12 of 12 books", [act("Search books", "fill", value="")]),
            state(shelf + "index.html", "Shelf\nWelcome to Shelf")),
        "search-book": (
            state(shelf + "catalog.html?q=The%20Glass%20Lighthouse", "Catalog\nThe Glass Lighthouse\nShowing 1 of 12 books",
                  [act("Search books", "fill", value="The Glass Lighthouse")]),
            state(shelf + "catalog.html", "Catalog\nShowing 12 of 12 books")),
        "filter-category": (
            state(shelf + "catalog.html?category=science", "Catalog\nThe Living Cell\nShowing 4 of 12 books",
                  [act("Category → Science", "select", value="science", current_value="Science")]),
            state(shelf + "catalog.html?category=fiction", "Catalog\nShowing 5 of 12 books",
                  [act("Category → Fiction", "select", value="fiction", current_value="Fiction")])),
        "sort-price": (
            state(shelf + "catalog.html?sort=price-asc", "Catalog",
                  [act("Sort by → Price low to high", "select", value="price-asc", current_value="Price low to high")]),
            state(shelf + "catalog.html?sort=title", "Catalog",
                  [act("Sort by → Title", "select", value="title", current_value="Title")])),
        "open-book-detail": (
            state(shelf + "book.html?id=the-clockmakers", "The Clockmakers\nPaul Mercier"),
            state(shelf + "book.html?id=winter-orchard", "Winter Orchard\nMara Voss")),
        "add-to-cart": (
            state(shelf + "cart.html", "Cart\nWinter Orchard\nTotal: $14.00"),
            state(shelf + "cart.html", "Your cart is empty.\nTotal: $0.00")),
        "contact-form": (
            state(shelf + "contact.html", "Contact", [
                act("Name", "fill", value="Ada Lovelace"), act("Email", "fill", value="ada@example.com"),
                act("Topic → Order", "select", value="order", current_value="Returns"),
                act("Message", "fill", value="Damaged cover")]),
            state(shelf + "contact.html", "Contact", [
                act("Name", "fill", value="Ada Lovelace"), act("Email", "fill", value="ada@example.org"),
                act("Topic → Order", "select", value="order", current_value="Order"),
                act("Message", "fill", value="")])),
        "contact-send": (
            state(shelf + "contact.html#sent", "Thanks, Grace Hopper. We will reply to grace@example.com."),
            state(shelf + "contact.html", "Contact\nWhere is my order")),
        "help-returns": (
            state(shelf + "help.html#returns", "Help\nReturns policy\nItems can be returned within 30 days."),
            state(shelf + "help.html", "Help\nShipping")),
        "empty-cart-total": (
            state(shelf + "cart.html", "Your cart is empty.\nTotal: $0.00"),
            state(shelf + "cart.html?seed=1", "Your cart is empty.\nTotal: $23.50")),
    }
    for test in tests:
        good, bad = pages[test["id"]]
        args = yaml.safe_dump(test["verify_args"], allow_unicode=True, sort_keys=False)
        add(f"demo {test['id']} passes", test["verify"], good, args)
        add(f"demo {test['id']} fails", test["verify"], bad, args)


def normalize_fixtures():
    samples = [
        "", "  ", "Hello   World", "\tTabs\nand\r\nnewlines ", "Zürich", "ZURICH", "Ça va, très bien", "Ñandú", "İstanbul", "ıi",
        "ẞ", "Straße", "ﬁ ﬂ ﬃ", "ℌ𝔢𝔩𝔩𝔬", "Ⅷ ⅷ", "ＡＢＣ　１２３", "①②③", "½", "㎏", "ǅ ǆ Ǆ", "ΟΔΟΣ ος σ Σ", "ᾳ ᾼ ᾈ",
        "\u00a0nbsp\u00a0", "a\u2003b\u2009c\u200ad", "a\x1cb\x1dc\x1ed\x1fe", "a\x85b", "a\u2028b\u2029c", "a\u3000b", "a\ufeffb",
        "مَرْحَبًا", "١٢٣ ٤٥٦", "עִבְרִית", "한글 조선", "ｶﾀｶﾅ", "क्षि हिन्दी", "ก็ ค่ะ", "日本語 テスト", "😀 👍🏽", "e\u0301 é", "a\u0308\u0323 ạ̈",
        "Å Å", "ǰ", "ŉ", "ǆ", "ᵃ ᵇ", "ⓐ", "™ ℠", "ﷺ", "ﬗ", "ẛ", "ῼ", "\u0345", "K K", "ſ",
    ]
    write("normalize.json", [{"input": s, "output": V.normalize(s)} for s in samples])
    sweep = {}
    ranges = list(range(0, 0x30000)) + list(range(0xE0000, 0xE1000))
    for cp in ranges:
        if 0xD800 <= cp <= 0xDFFF:
            continue
        char = chr(cp)
        out = V.normalize(char)
        if out != char:
            sweep[f"{cp:x}"] = out
    write("normalize_codepoints.json", sweep)
    (OUT / "normalize_meta.json").write_text(json.dumps({"python_unicode": unicodedata.unidata_version, "ranges": ["0-2ffff", "e0000-e0fff"]}) + "\n")


def unquote_fixtures():
    samples = [
        "", "plain", "a+b", "a%20b+c", "%E2%82%AC", "%e2%82%ac", "%E2%82", "%E2%28", "%FF", "%C3%A9t%C3%A9", "%zz", "%4", "100%", "%%41",
        "é%C3%A9", "%C3é%A9", "a%2Bb", "%41%42%43", "%F0%9F%98%80", "%F0%9F%98", "%ED%A0%80", "%C0%80", "%F4%90%80%80", "%E0%80%80",
        "%C3%28", "%E2%82%AC%E2%82", "%80", "%C2", "%C2%80", "x%00y", "%25", "%2525", "https://x.test/a%2Fb?q=%C3%BC+ber#f%C3%A9",
        "日本%E6%97%A5", "%F1%80%80", "%F1%80%80%41", "%E1%80", "%E1%80%41", "%F0%80", "%F0%90%80%41", "+%2B+",
    ]
    write("unquote.json", [{"input": s, "output": unquote_plus(s)} for s in samples])


def url_fixtures():
    urls = [
        "https://www.google.com/travel/flights/search?tfs=abc#f", "http://a.com", "http://a.com/", "http://a.com?x=1#f", "http://a.com#f?x",
        "https://user:pw@Host.Example.com:8080/p/a/t/h;p=1?q=1&r=2#frag", "https://[::1]:8080/x", "https://[fe80::1%25en0]/x", "//x.test/p",
        "/relative/path?x=1", "relative", "?q=1", "#frag", "", "  https://lead.space/x", "https://tab\t.test/\nx", "mailto:a@b.com", "data:text/html,hi",
        "javascript:void(0)", "about:blank", "HTTP://UPPER.TEST/Path", "http://a.com/a;b/c;d?x", "http://a.com/a;b/c/d?x", "file:///etc/hosts",
        "ftp://f.test/a;type=i", "localhost:3000/x", "http://a.com:/x", "http://:80/x", "http://@a.com/x", "http://a.com/x?a=1?b=2#c#d",
        "https://news.ycombinator.com/item?id=22", "https://example.com/p%C3%A4ge", "https://a.com//double//slash//", "1abc://x.test/",
        "a+b-c.d://x.test/y", "a_b://x.test/y", "https://x.test:99999/", "http://a.com/;", "http://a.com;x/y",
    ]
    rows = []
    for url in urls:
        try:
            p = urlparse(url)
            rows.append({"url": url, "scheme": p.scheme, "netloc": p.netloc, "hostname": p.hostname, "path": p.path,
                         "params": p.params, "query": p.query, "fragment": p.fragment})
        except ValueError:
            continue
    write("urlparse.json", rows)

    bases = ["https://news.ycombinator.com/", "https://news.ycombinator.com/a/b/c?x=1#f", "http://a.com", "https://a.com/dir/", "https://a.com/dir/page.html;p=1?q=2",
             "file:///a/b", "mailto:x@y.z", "ftp://f.test/a/b", ""]
    refs = ["https://a.com/post/", "item?id=22", "../x", "../../../x", "./y", "y/../z", "/abs?x=1", "//cdn.example.org/p/", "?q=3", "#top", "", "https://a.com",
            "http://b.com/x", "javascript:void(0)", "mailto:a@b.c", "a//b", "..", ".", "dir/.", "dir/..", "x;p=2?y", "https://a.com/x;p=1", "vote?id=1&how=up", " space", "d/e/../f/", "g//h"]
    joins = []
    for base in bases:
        for ref in refs:
            try:
                joins.append({"base": base, "ref": ref, "joined": urljoin(base, ref)})
            except ValueError:
                continue
    write("urljoin.json", joins)

    pairs = [
        ("https://a.com/post", "https://a.com/post"), ("https://www.a.com/post/", "https://a.com/post"), ("https://A.COM/post", "https://a.com/post"),
        ("https://a.com:8080/post", "https://a.com/post"), ("https://a.com/post?x=1", "https://a.com/post"), ("https://a.com/post#f", "https://a.com/post"),
        ("https://a.com/Post", "https://a.com/post"), ("https://a.com/post//", "https://a.com/post"), ("http://a.com/post", "https://a.com/post"),
        ("https://wwwa.com/post", "https://a.com/post"), ("https://a.com/p%C3%A4ge", "https://a.com/päge"), ("https://a.com", "https://a.com/"),
        ("https://www.www.a.com/", "https://www.a.com/"), ("", ""), ("https://a.com/x;p=1", "https://a.com/x"), ("/x", "/x"), ("https://user@a.com/x", "https://a.com/x"),
    ]
    write("sameurl.json", [{"a": a, "b": b, "same": V.same_url(a, b)} for a, b in pairs])

    queries = ["tfs=abc", "tfs=", "tfs", "a=1&tfs=x&tfs=y", "tfs=a+b", "tfs=%E2%82%AC", "TFS=x", "t%66s=z", "a=1;tfs=x", "&&tfs=q&&", "tfs=1=2", "x=1", "", "tfs=%zz", "tfs=%20",
               "tfs=&tfs=second", "a%26b=1&tfs=w"]
    write("query.json", [{"query": q, "value": parse_qs(q).get("tfs", [""])[0]} for q in queries])


def base64_fixtures():
    needle = "2026-10-12"

    def python(encoded):
        try:
            return needle.encode() in base64.urlsafe_b64decode(encoded + "=" * (-len(encoded) % 4))
        except ValueError:
            return False

    good = base64.urlsafe_b64encode(b"xx" + needle.encode() + b"yy").decode()
    samples = [
        good, good.rstrip("="), base64.b64encode(b"\xfb\xff" + needle.encode()).decode(), base64.urlsafe_b64encode(b"\xfb\xff" + needle.encode()).decode(),
        "", "A", "AA", "AAA", "AAAA", "AAAA=", "AAAA==", "=", "==", "A=", "A==", "AB=", "AB==", "ABC=", "ABC==", "AB=C", "AB==CD", "AB CD", "AB\nCD", "A*B*C*D*",
        "ünï", good + "!!", "!" + good, good[:4] + "=" + good[4:], good + "=" * 5, "-_-_", "+/+/", "+_+_", "-/-/",
        base64.urlsafe_b64encode(needle.encode()).decode(), base64.urlsafe_b64encode(needle.encode()).decode().rstrip("="),
        base64.urlsafe_b64encode(b"a" + needle.encode()).decode(), base64.urlsafe_b64encode(b"ab" + needle.encode()).decode().rstrip("="),
    ]
    write("base64.json", [{"encoded": s, "needle": needle, "contains": python(s)} for s in samples])


def date_fixtures():
    days = [date(2026, 1, 1), date(2026, 9, 28), date(2026, 10, 12), date(2024, 2, 29), date(2026, 12, 31), date(2027, 1, 3), date(2021, 1, 3),
            date(2020, 12, 31), date(2026, 3, 9), date(1999, 7, 4), date(2100, 2, 28), date(999, 5, 5), date(2026, 11, 7), date(2020, 1, 1), date(2018, 12, 31)]
    write("dateforms.json", [{"day": d.isoformat(), **V.date_forms(d)} for d in days])

    directives = "YyCmdejBbhAauwUWVGgFDHMSIpTRnt%"
    formats = ["%" + c for c in directives] + [f"%-{c}" for c in "dmejyCUWVgHMSIuw"] + [
        "%A, %B %-d, %Y", "%d/%m/%Y", "%Y%m%d", "%b %d", "week %V of %G", "%-m/%-d/%y", "100%%", "no directives", "%Y-%m-%d %H:%M", "%-Y", "%-B", "%-p",
    ]
    rows = []
    for d in days:
        if d.year < 1000:
            continue
        for fmt in formats:
            rows.append({"day": d.isoformat(), "format": fmt, "output": d.strftime(fmt)})
    write("strftime.json", rows)
    write("strftime_unsupported.json", ["%c", "%x", "%X", "%r", "%Z", "%z", "%f", "%s", "%k", "%l", "%P", "%E", "%Q", "%", "abc%", "%-", "%_d", "%0d", "%^a", "%#d", "%+", "%N"])

    placeholder = re.compile(r"\{([^{}]*)\}")
    token = re.compile(r"(date|weekday)\+(\d+)(?::(.+))?")

    def resolve(text, today):
        def replace(match):
            parsed = token.fullmatch(match.group(1))
            if not parsed or (parsed.group(1) == "weekday" and parsed.group(3)):
                raise ValueError(f"Unknown placeholder {match.group(0)!r} in {text!r}")
            kind, offset, fmt = parsed.groups()
            day = today + timedelta(days=int(offset))
            if kind == "weekday":
                return V.DAYS[day.weekday()]
            return day.strftime(fmt) if fmt else f"{V.MONTHS[day.month - 1]} {day.day}, {day.year}"
        return placeholder.sub(replace, text)

    texts = [
        "on {date+10}", "{date+10:%Y-%m-%d}", "{weekday+3}", "{date+4}", "{date+0}", "{date+0:%A}", "{date+365}", "{date+3:%B %-d}", "no placeholders", "",
        "{date+1} and {date+2:%d} and {weekday+0}", "literal { brace", "a } b", "{{date+1}}", "{}", "{date+1:%Y}{date+2:%Y}", "{date+007}", "{date+1:%%}",
        "{month+1}", "{weekday+1:%A}", "{date-1}", "{date+x}", "{date+1:}", "{Date+1}", "{date+1 }", "{ date+1}", "{date+1:%Y\n}", "Find flights on {date+14}, return {date+21:%a}",
        "\\d{4}", "x{2,3}y", "{date+2:%e}",
    ]
    rows = []
    for today in (TODAY, date(2026, 12, 30), date(2028, 2, 27)):
        for text in texts:
            try:
                rows.append({"today": today.isoformat(), "text": text, "output": resolve(text, today), "error": None})
            except ValueError as error:
                rows.append({"today": today.isoformat(), "text": text, "output": None, "error": f"ValueError: {error}"})
    write("resolve.json", rows)


def repr_fixtures():
    strings = ["", "abc", "it's", 'say "hi"', "it's \"x\"", "tab\t", "nl\n", "cr\r", "\x00", "\x1f", "\x7f", "é", "\u00a0", "\u200b", "日本", "😀", "\U000e0001", "back\\slash",
               "\u0085", "\ufeff", "\u2028", "\u3000", "\ufffd", "a'b\"c", "'", '"', "\U0010ffff", "\u0378", "\ud7ff", "e\u0301", "\xad", "\u00ff", "\u0100"]
    write("repr_strings.json", [{"value": s, "repr": repr(s)} for s in strings])
    floats = [0.0, -0.0, 1.5, 1e16, 1e15, 1.2345e-5, 0.0001, 123456789012345678.0, 3.0, 1e22, 2.5e-10, 100.0, 1e100, 5e-324, 1.7976931348623157e308,
              0.1 + 0.2, 1 / 3, 1234567.125, 0.00012345, 9999999999999998.0, 1e-4, 1e-5, 12345678901234567.0, float("inf"), float("-inf"), float("nan"), 2.0**53, 1.5e300, -2.5]
    write("repr_floats.json", [{"hex": f.hex(), "repr": repr(f)} for f in floats])

    snippets = ["hello", '"quoted"', "'single'", "42", "-7", "+5", "0", "007", "0o17", "0x1F", "0b101", "1_000", "12:30", "1:02:03", "1.50", "1.0", ".5", "1e3", "1.5e+3",
                "1.5e3", ".inf", "-.inf", ".nan", "yes", "No", "TRUE", "off", "on", "y", "n", "~", "null", "Null", "", "'null'", '"1"', "08", "09.5", "1,000", '"0x1F"', "12345678901234567890",
                "1__0", "-0", "0.", "+.5", "-1.5e-7", "1:30:00.5", "True", "tRUE", "yes please", "0_", "0b", "1e+400", "0.1", "3.14159", "100.0", "1e-3", "1.e+5", "10:00"]
    snippets += ["!!str yes", "!!str 42", "!!str null", "!!str ~", '!!str "x y"', "!!str", "!!int 12", '!!int "12"', "!!int 0x1F", "!!int 1_000", "!!int 1:30",
                 "!!int abc", "!!int 1.5", "!!float 1", "!!float 1e3", "!!float .inf", "!!float 1:30.5", "!!float abc", "!!bool yes", "!!bool Off", "!!bool TRUE",
                 "!!bool maybe", "!!null x", "!!null", '!!null ""', "!!binary aGk=", "!!timestamp 2026-09-28", "!custom value", "!!seq [a]", "!!map {a: b}",
                 "!<tag:yaml.org,2002:str> yes"]
    rows = []
    for snippet in snippets:
        try:
            value = yaml.safe_load("v: " + snippet)["v"]
        except Exception as error:
            rows.append({"yaml": snippet, "kind": "error", "str": "", "repr": "", "error": type(error).__name__})
            continue
        kind = {str: "string", bool: "bool", int: "int", float: "float", type(None): "null"}.get(type(value))
        if kind is None:
            rows.append({"yaml": snippet, "kind": "other", "str": "", "repr": "", "error": type(value).__name__})
            continue
        rows.append({"yaml": snippet, "kind": kind, "str": str(value), "repr": repr(value), "error": ""})
    write("scalars.json", rows)


def regex_fixtures():
    def at(path):
        return "https://x.test" + path

    base = r"^https://x\.test"
    pairs = [
        # \d
        (r"/order/\d+$", at("/order/%D9%A3")), (r"/order/\d+$", at("/order/12")), (r"/order/\d$", at("/order/%D9%A3")),
        (r"/order/\d{2}$", at("/order/%D9%A1%D9%A2")), (r"/o/\d$", at("/o/%EF%BC%91")), (r"/o/\d$", at("/o/%DB%B1")),
        (r"/o/\d$", at("/o/%E2%91%A0")), (r"/o/\d$", at("/o/%C2%B2")), (r"/o/\d$", at("/o/%E2%85%A7")), (r"/o/\d$", at("/o/x")),
        (r"/o/\d$", at("/o/%E0%A5%A9")), (r"\d{4}-\d{2}", at("/d/2026-09")), (r"\d{4}-\d{2}", at("/d/%DB%B2%DB%B0%DB%B2%DB%B6-%DB%B0%DB%B9")),
        # \D
        (r"^https://x\.test/\D+$", at("/abc")), (r"^https://x\.test/\D$", at("/%D9%A3")), (r"^https://x\.test/\D$", at("/%E2%91%A0")),
        (r"/id/\D\d", at("/id/a1")), (r"/id/\D\d", at("/id/%C3%A91")), (r"^https://x\.test/\D*$", at("/")),
        # \w
        (r"/caf\w$", at("/caf%C3%A9")), (r"/caf\w$", at("/caf-")), (r"/caf\w$", at("/caf%0A")), (base + r"/\w+$", at("/r%C3%A9sum%C3%A9")),
        (base + r"/\w+$", at("/a-b")), (base + r"/\w+$", at("/a_b")), (r"/\w$", at("/%E2%91%A0")), (base + r"/\w+$", at("/e%CC%81")),
        (base + r"/\w{2}$", at("/%E6%97%A5%E6%9C%AC")), (base + r"/\w+$", at("/%CE%B1%CE%B2%CE%B3")), (base + r"/\w+$", at("/%D8%B3%D9%84%D8%A7%D9%85")),
        (base + r"/\w$", at("/%F0%9F%98%80")), (base + r"/\w$", at("/%E2%80%94")), (base + r"/\w+$", at("/%E0%A4%A8%E0%A4%AE%E0%A4%B8%E0%A5%8D%E0%A4%A4%E0%A5%87")),
        (base + r"/\w+$", at("/%C2%AA")), (base + r"/\w$", at("/%C2%B5")), (base + r"/\w$", at("/%CA%B0")), (r"^\w+://", at("/")),
        # \W
        (r"/a\W$", at("/a%C3%A9")), (r"/a\W$", at("/a-")), (r"/a\W$", at("/a%C2%A0")), (r"/a\W", at("/a%E2%80%94")), (r"/a\W$", at("/a_")),
        (r"/a\W$", at("/a%F0%9F%98%80")), (r"/\W\W$", at("/--")),
        # \s
        (r"a\sb", at("/a%C2%A0b")), (r"a\sb", at("/a%E3%80%80b")), (r"a\sb", at("/a%E2%80%8Bb")), (r"a\sb", at("/a%0Bb")), (r"a\sb", at("/a%1Cb")),
        (r"a\sb", at("/a+b")), (r"a\sb", at("/a%C2%85b")), (r"a\sb", at("/a%E1%A0%8Eb")), (r"a\sb", at("/a%E2%80%A8b")), (r"a\sb", at("/a%EF%BB%BFb")),
        (r"a\sb", at("/a%E2%80%AFb")), (r"a\sb", at("/ab")), (r"a\s+b", at("/a%20%C2%A0%20b")), (r"a\sb", at("/a%1Fb")), (r"a\sb", at("/a%1Bb")),
        # \S
        (r"^https://x\.test/a\Sb$", at("/a+b")), (r"^https://x\.test/a\Sb$", at("/a%C2%A0b")), (r"^https://x\.test/a\Sb$", at("/a-b")),
        (r"^https://x\.test/a\Sb$", at("/a%E2%80%8Bb")), (r"^https://x\.test/\S+$", at("/abc")),
        # character classes
        (r"/o/[\d]$", at("/o/%D9%A3")), (r"/o/[^\d]$", at("/o/%D9%A3")), (r"/o/[^\d]$", at("/o/x")), (r"/o/[\w-]+$", at("/o/caf%C3%A9-x")),
        (r"/o/[\W]$", at("/o/%C3%A9")), (r"/o/[\W]$", at("/o/-")), (r"/o/[\S]$", at("/o/%C2%A0")), (r"/o/[\S]$", at("/o/x")),
        (r"/o/[\D\s]$", at("/o/%D9%A3")), (r"/o/[\D\s]$", at("/o/+")), (r"/o/[a\W]$", at("/o/a")), (r"/o/[a\W]$", at("/o/%C3%A9")),
        (r"/o/[a\W]$", at("/o/-")), (r"/o/[\w\s]+$", at("/o/a%C2%A0%C3%A9")), (r"/o/[^\W]$", at("/o/%C3%A9")), (r"/o/[^\W]$", at("/o/-")),
        (r"/o/[^\S]$", at("/o/%C2%A0")), (r"/o/[^\s]$", at("/o/%C2%A0")), (r"a[\b]b", at("/a%08b")), (r"a[\b]b", at("/ab")), (r"[\w.]+@", at("/x.y@z")),
        (r"[]a]+$", at("/a]")), (r"[^]a]+$", at("/a]")), (r"[^]a]$", at("/b")), (r"/o/[$]", at("/o/$")), (r"/o/[\]]", at("/o/]")), (r"/o/[\\]", at("/o/%5C")),
        (r"x[[:alpha:]]", at("/xb]")), (r"x[[:alpha:]]", at("/x:]")), (r"/o/[[]", at("/o/[")), (r"/o/[a-c\d]+$", at("/o/a%D9%A3c")),
        # $ and \Z
        (r"done$", at("/done%0A")), (r"done$", at("/done")), (r"done$", at("/done%0A%0A")), (r"done\Z", at("/done%0A")), (r"done\Z", at("/done")),
        (r"done$", at("/done%0Ax")), (r"(a|done$)", at("/done%0A")), (r"^https://x\.test/(done|ok)$", at("/ok%0A")), (r"done$|nope", at("/done%0A")),
        (r"a$b", at("/a%0Ab")), (r"done$$", at("/done%0A")), (r"[$]", at("/a$b")), (r"a\$", at("/a$")), (r"a\$$", at("/a$%0A")), (r"a\\$", at("/a%5C%0A")),
        (r"(?m)b$", at("/a%0Ab%0Ac")), (r"(?m)^b$", at("/a%0Ab%0Ac")), (r"b$", at("/a%0Ab%0Ac")), (r"(?m:b$)", at("/a%0Ab%0Ac")), (r"(?m)c$", at("/a%0Ab%0Ac")),
        (r"(?i)DONE$", at("/done%0A")), (r"(?ms)b.$", at("/b%0A")), (r"(?-m:b$)", at("/a%0Ab%0Ac")), (r"(?im)^B$", at("/a%0Ab%0Ac")), (r"(?s)a.b$", at("/a%0Ab%0A")),
        (r"(x(?m:b$)y)", at("/xb%0Ay")), (r"(?:done$)", at("/done%0A")), (r"^$", "https://x.test"), (r"$", at("/x")),
        # \A, ^, flags
        (r"\Ahttps", at("/")), (r"x\Ahttps", at("/")), (r"^https://x\.test/a$", at("/a")), (r"^https://x\.test/a$", at("/a%0A")), (r"(?s)a.b", at("/a%0Ab")),
        (r"a.b", at("/a%0Ab")), (r"a.b", at("/a%20b")), (r"a[^x]b", at("/a%0Ab")),
        # {,n}
        (r"^https://x\.test/a{,2}$", at("/aa")), (r"^https://x\.test/a{,2}$", at("/aaa")), (r"^https://x\.test/a{,2}$", at("/a{,2}")),
        (r"^https://x\.test/a{,}$", at("/aaaa")), (r"^https://x\.test/a{,}$", at("/a{,}")), (r"^https://x\.test/\d{,3}$", at("/%D9%A1%D9%A2")),
        (r"^https://x\.test/a{,0}$", at("/")), (r"^https://x\.test/a{2,}$", at("/aaa")), (r"^https://x\.test/a{2}$", at("/aa")),
        (r"^https://x\.test/a{}$", at("/a{}")), (r"^https://x\.test/a{x}$", at("/a{x}")), (r"^https://x\.test/a{1,x}$", at("/a{1,x}")),
        (r"^https://x\.test/a{ 1}$", at("/a{ 1}")), (r"^https://x\.test/(ab){,2}c$", at("/ababc")), (r"^https://x\.test/(ab){,2}c$", at("/abababc")),
        (r"^https://x\.test/[ab]{,2}$", at("/ba")), (r"^https://x\.test/\w{,3}$", at("/%C3%A9%C3%A9")),
        # case, plain constructs, alternation, groups, escapes
        (r"/ÉTÉ$", at("/%C3%A9t%C3%A9")), (r"/été$", at("/%C3%89T%C3%89")), (r"k", at("/%E2%84%AA")), (r"s", at("/%C5%BF")),
        (r"ss", at("/%C3%9F")), (r"/\w+\.html$", at("/caf%C3%A9.html")), (r"^https://x\.test/(a|b)+$", at("/abba")), (r"(?:a|b)c", at("/bc")),
        (r"/(?P<n>ab)\.", at("/ab.")), (r"a.c", at("/abc")), (r"a\.c", at("/abc")), (r"a\\b", at("/a%5Cb")), (r"\(x\)", at("/(x)")), (r"a\x2fb", at("/a/b")),
        (r"a\tb", at("/a%09b")), (r"a\nb", at("/a%0Ab")), (r"colou?r", at("/color")), (r"a+?b", at("/aab")), (r"a*$", at("/aaa")), (r"^https", at("/")), (r"$^", at("/")),
        (r"(", at("/")), (r"a{1", at("/a{1")), (r"a**", at("/a")), (r"[z-a]", at("/a")),
        (r"(?u)caf\w$", at("/caf%C3%A9")), (r"(?s:a.b)$", at("/a%0Ab")),
    ]
    boundary = [
        (r"\bsum", at("/r%C3%A9sum%C3%A9")), (r"\bsum", at("/sum")), (r"cart\b", at("/cart")), (r"cart\b", at("/carte")), (r"\Bx", at("/ax")), (r"(\bfoo|bar\b)", at("/foo")),
        (r"a[\b]b\b", at("/a%08b")), (r"\b\d+\b", at("/12")), (r"\bcaf\b", at("/caf%C3%A9")), (r"(?i)\bABC\b", at("/abc")),
    ]
    rows = []
    for pattern, url, rejected in [(p, u, False) for p, u in pairs] + [(p, u, True) for p, u in boundary]:
        row = {"pattern": pattern, "url": url, "boundary": rejected}
        try:
            row.update(matches=V.page(state(url), url=pattern)["passed"], error=None)
        except re.error as error:
            row.update(matches=None, error=f"re.error: {error}")
        rows.append(row)
    write("regex.json", rows)

    def ranges(test):
        found, start = [], None
        for code in range(0x110000):
            hit = not 0xD800 <= code <= 0xDFFF and test(chr(code))
            if hit and start is None:
                start = code
            elif not hit and start is not None:
                found.append([start, code - 1])
                start = None
        if start is not None:
            found.append([start, 0x10FFFF])
        return found

    classes = {
        "unicode": unicodedata.unidata_version,
        "word": ranges(lambda c: re.match(r"\w", c) is not None),
        "digit": ranges(lambda c: re.match(r"\d", c) is not None),
        "space": ranges(lambda c: re.match(r"\s", c) is not None),
        "assigned": ranges(lambda c: unicodedata.category(c) != "Cn"),
    }
    (OUT / "regex_classes.json").write_text(json.dumps(classes) + "\n", encoding="utf-8")
    print(f"regex_classes.json: {len(classes['word'])} word ranges")


if __name__ == "__main__":
    page_cases()
    hn_cases()
    flights_cases()
    demo_cases()
    write("cases.json", CASES)
    normalize_fixtures()
    unquote_fixtures()
    url_fixtures()
    base64_fixtures()
    date_fixtures()
    repr_fixtures()
    regex_fixtures()
