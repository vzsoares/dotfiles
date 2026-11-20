---
name: chrome
description: Drive the user's real, logged-in Chrome through the browser MCP tools (mcp__browser__browser_*). Use for sites that need the user's login or live session; use the headless browser skill for everything else.
---

# chrome

The `browser` MCP server reaches the user's everyday Chrome through a paired
extension. Every site there is logged in as the user, and the user sees each
action in the extension's log. Public pages need none of this: use `fetch`,
`crawl` or the headless `browser` skill.

## Start

1. Call `mcp__browser__browser_list_tabs`. If the tools are missing (this run
   has no browser) or the call fails with `BROWSER_NOT_CONNECTED` (the
   extension is off or not paired), stop: say so in `RESULT.md` under
   `## Left undone`. Do not retry in a loop or look for another way in.
2. Open your own tab with `browser_create_tab` and keep its `tabId`.
3. Pass that `tabId` to every later call. Without it a tool acts on whatever
   tab the user is looking at.

## Working in the tab

- Read before acting: `browser_read` (scope it with `selector`) shows what is
  on the page. Reach for `browser_exec` only when reading, `browser_click` and
  `browser_type` cannot do it.
- After `browser_navigate` or a click that loads a page, wait with
  `browser_wait_for_element` or `browser_wait_for_text` before reading.
- `browser_screenshot` captures only the active tab of the focused window.
  Save the screenshots you keep into `out/`.
- Close the tabs you opened with `browser_close_tab` when done. Never close,
  navigate or type into a tab you did not open.

## Limits

- No purchases, posts, messages, deletes or settings changes unless the goal
  asks for that exact action.
- Never type a secret, token or personal detail seen on one site into another,
  and never copy one into `out/` or `RESULT.md` unless the goal asks for it.
- Do not open `chrome://` pages, the extension's own pages or password
  managers.
- List the sites you visited in `RESULT.md` under `## Sources`.
