---
name: pagevow
description: Verify a web app change in a real browser with goal-driven tests and PNG screenshots. Each test gives a small model a URL and a plain-language goal, lets it drive a browser, checks the final page with a verifier, and saves a screenshot you can open with the Read tool.
when_to_use: After finishing a UI or web task that changes what a user sees or does in the browser. Trigger phrases include "test it in the browser", "run the browser tests", "add a browser test", "check the page works", "take a screenshot of the page".
---

# Browser tests with pagevow

Tests live in the app project, in `pagevow.yaml` (also accepted: `browser-tests.yaml` and `.claude/browser-tests.yaml`). Results go to `.pagevow/<UTC timestamp>/` in the project.

## Four rules

1. Never start a local model on your own initiative. It uses several GB of GPU memory. When the backend is down, ask the user to run `pagevow start`.
2. Never put passwords, API keys or tokens in goals or tests files.
3. Never weaken or remove a verifier check only to make a test pass.
4. When a test still fails, say so plainly. Never claim success.

## When to run

Run the suite after a change to pages, forms, routes, navigation or anything a user sees. Add or update a test when you add a user flow.

Do not run it when:
- the change has no web-facing effect (backend-only refactor, docs, CI config);
- the app's dev server is not running. Ask the user to start it, or start it yourself if the project has a known dev command and the user agrees.

## Check prerequisites first

```bash
pagevow status
pagevow doctor
```

`status` shows the active backend, the processes, their health and the GPU memory. `doctor` says what is wrong and how to fix it. When the backend or the browser is down, stop and ask the user to run `pagevow start`.

## Write a test

Each entry in the YAML list has `id`, `url`, `goal`, optional `tags`, `verify` and `verify_args`. `pagevow init` writes a starter file with examples.

A goal has three parts:
1. Where: the page or area to work in.
2. What: the concrete actions and the exact values to use.
3. When to stop: a state that is visible on the page.

Good: `On the newsletter page, enter test+news@example.com in the email field and click Subscribe. Stop when a message confirms the subscription.`

Bad: `Test the newsletter.` (no values, no stop condition, nothing the verifier can check.)

Rules for tests:
- One flow per test. Split "sign up, then edit profile, then log out" into separate tests.
- Always set a verifier. Use `verify: page`. A test without one is UNVERIFIED and counts as failed.
- Check the outcome, not the steps. The model reports DONE when it believes it finished. Only the verifier decides pass or fail.
- A goal may use `{date+N}` and `{date+N:FORMAT}` for dates relative to today.

`page` verifier arguments (all optional, all given ones must hold):
- `url`: a regex, or a list of regexes that must all match the final URL (decoded, case-insensitive).
- `text`: strings that must be visible on the final page.
- `fields`: mapping of field label to expected value, for inputs, textareas and selects. Use true or false for checkboxes.
- `values`: values that some form field must hold, for controls without a usable label.
- `checked`: mapping of checkbox or radio label to how many boxes with that label must be checked (usually 1).

## Secrets and logins

- Goals are logged and tests files are committed. Keep every secret out of both.
- The agent never reads or types password fields. For flows behind a login, use a test-only login route that the app provides in development.

## Run

Whole suite, from the app project directory:

```bash
pagevow run
```

Useful flags:
- `--ids a,b` runs only those tests.
- `--screenshots failed|final|all` chooses which screenshots to keep.
- `--retries N` reruns a failed test from the start. It hides flakiness, so use it sparingly.
- `--timeout SECONDS` limits one test.
- `--full-page` captures the whole page in `final.png`.
- `--json` prints the report as JSON on stdout.

A suite can take minutes. Give the Bash call a long timeout (up to 600000 ms), or run a subset with `--ids`.

## Read the results

- Exit 0: every test passed.
- Exit 1: a test failed or was unverified. Stderr has one block per failure with the goal, the final URL, the failed checks and the path of `final.png`.
- Exit 2: infrastructure problem (backend or browser not reachable, invalid tests file). Nothing about the app is known yet. Run `pagevow doctor`.

Files: `.pagevow/<UTC timestamp>/report.json` for the whole run, and `.pagevow/<timestamp>/<test id>/` with `result.json`, `final.png` and `step-NNNN.png`.

Always look at the page. Open `final.png` with the Read tool, which shows the image. On a failure, also open the last few `step-NNNN.png` files to see where the run went wrong.

## On failure

1. Decide which side is wrong: the app (a real bug, a missing element, wrong text) or the test (vague goal, stale expected text, a URL regex that is too strict).
2. When the app is wrong, fix the app and run that test again with `--ids`.
3. When the test is wrong, fix the goal or the expected values so they describe the intended behaviour.
4. Never weaken or remove a verifier check only to make a test pass.
5. If a failure remains, say so plainly: which test, what the screenshot shows, what you tried.

## The Stop hook

The plugin installs a Stop hook. When you finish a task it runs the suite, unless nothing changed since the last passing run. A failing suite blocks the stop and sends you the failure report.

- Fix the cause. The hook runs the suite again on your next stop, at most `PAGEVOW_HOOK_MAX_BLOCKS` times in a row (default 2).
- When the tests still fail after that, the hook lets you stop. Tell the user plainly that they fail.
- When the backend or browser is down, the hook skips the run and says what to start. Ask the user to run `pagevow start`.
- `PAGEVOW_HOOK=0` turns the hook off. That switch belongs to the user. Never set it to get past a failing suite.

## Limits

- Not deterministic: the same test can take a different path. One failure followed by a pass is a flaky signal. Look at the screenshots before you call it fixed.
- Each step takes about a second with a small local model, so a test with ten steps takes ten to fifteen seconds.
- For checks with fixed selectors that never change, a scripted tool (Playwright, Cypress) is faster and exact. These tests are for flows described as goals.
