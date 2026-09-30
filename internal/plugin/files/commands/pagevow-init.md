---
description: Create a starter pagevow.yaml tests file
allowed-tools: Bash(pagevow:*), Read, Edit
---

Create the browser tests file for this project.

1. Run `pagevow init`. It writes a starter `pagevow.yaml` and never overwrites an existing tests file.
2. Read the app's routes and pages, then edit the starter file so it describes the app's real flows.
3. Write one flow per test. Give each test a goal with a place, the exact values to use and a stop condition that is visible on the page. Set `verify: page` with checks on the final page.
4. Put no passwords, keys or tokens in the file. For a flow behind a login, use a test-only login route that the app provides in development.
5. Tell the user which flows you covered and which you left out.
