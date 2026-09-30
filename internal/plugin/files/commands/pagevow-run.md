---
description: Run the browser tests with pagevow and read the screenshots
allowed-tools: Bash(pagevow:*), Read
---

Run the project's browser tests and report what the pages show.

1. Run `pagevow run $ARGUMENTS` from the project directory. When the user named tests, add `--ids` with their ids. Give the Bash call a long timeout, up to 600000 ms.
2. Open the `final.png` of every failing test with the Read tool. Read the last few `step-NNNN.png` files of a test when the cause is not clear.
3. Follow the On failure steps of the pagevow skill: decide whether the app or the test is wrong, fix that side, and run the failing tests again with `--ids`.
4. If `pagevow run` exits with code 2, the backend or the browser is not reachable. Run `pagevow status`, then ask the user to run `pagevow start`. Never start a local model yourself.
5. If a test still fails at the end, say which test, what its screenshot shows and what you tried. Do not claim success.
