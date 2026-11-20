# pi-box eval rubric

Ten tool-call tasks (`tasks.json`) run once on each model in `models.json`:
GLM 5.3 Flash Uncensored, DeepSeek V4.1 Flash Uncensored and Gemma 4
Uncensored on Venice, `--thinking low`. Each task runs in its own folder under
`~/pi-box/work/eval-898/runs/<stamp>/<model>/<task>/`, and `eval.py` scores it
from the files it leaves in `out/`. No human grading.

## Tasks

| id | skill | proves |
|---|---|---|
| fetch-text | fetch | one page to clean text |
| fetch-headers | fetch | curl status and headers into JSON |
| crawl-books | crawl | two paginated pages, delay honoured, CSV |
| browser-js | browser | a JavaScript-rendered page via headless Chromium |
| extract-html | extract | htmlq over a local table, number cleanup |
| extract-json | extract | a jq filter and sum over a local file |
| file-write | none | write a script, run it, save its output |
| media-tone | media | ffmpeg generate and encode, ffprobe duration |
| multi-authors | fetch | fetch, dedupe, sort, count, two consistent files |
| multi-recover | fetch | handle a 404, then fall back |

The public targets are `example.com`, `iana.org` and the scraping practice
sites `books.toscrape.com` and `quotes.toscrape.com`, a handful of requests each.

## Score per call (0 to 3)

| score | when |
|---|---|
| 3 | every check passes, the final message ends with `WORKER_DONE <task>`, no tool call failed |
| 2 | every check passes, but a tool call failed or the marker is missing |
| 1 | a check fails (or the call was stopped), but at least one tool call succeeded |
| 0 | no successful tool call: a refusal, no tool use, a crash |

Per model: the score out of 30, passes (score 2 or 3), cost and tool calls.

## Default model

`glm-5-3-flash-uncensored` stays the default when its score is within 2
points of the best. Otherwise the best score wins, the lower cost breaking a
tie. `results.md` names the pick; the orchestrator confirms it on the task.

## Cost

- Prices (USD per 1M tokens, Venice's model list, 2026-11-20): GLM in 0.165 /
  out 0.55, DeepSeek 0.33 / 1.32, Gemma 0.1625 / 0.50. Cache reads are priced
  as full input on purpose, so the tally can only overstate.
- The eval's `models.json` caps each message at 4096 output tokens and the
  context at 65536, so one message costs at most $0.027 (DeepSeek).
- A call is killed (`docker kill pi-box-eval-898`) once two more messages
  could push it past its $0.10 cap, and is killed after 600 s.
- A call starts only when $0.10 more still fits under the $3 total, so the
  batch stops before any call that could cross it. A killed call counts as at
  least $0.10. 30 calls of $0.10 fill the cap exactly; the estimate is ~$0.30.
- Each message's cost is the larger of pi's `usage.cost.total` and the token
  counts times these prices.
- Backstop: the Venice key's prepaid credit (at most $10, auto-reload off).
