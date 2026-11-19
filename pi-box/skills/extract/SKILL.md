---
name: extract
description: Pull structured data out of saved HTML, JSON, text or files with htmlq, jq, rg, fd, sqlite3, pandoc and pandas. Use after fetch, crawl or browser saved something.
---

# extract

Work on files already saved under `out/`; do not re-download to re-parse.

## HTML

```bash
htmlq --text 'h1' < out/page.html                  # text of a selector
htmlq --attribute href 'a' < out/page.html          # every link
htmlq --base "$URL" --attribute href 'a' < out/page.html  # absolute links
htmlq 'table' < out/page.html | pandoc -f html -t csv -o out/table.csv
```

Main-article text: `trafilatura` (see `fetch`).

## JSON, text, files

```bash
jq -r '.items[] | [.id, .name] | @csv' out/data.json > out/data.csv
rg -n --no-heading 'pattern' out/              # search saved pages
fd -e html . out/                              # list files by extension
```

## Tabular and SQL

```bash
python -c "import pandas as pd; df = pd.read_csv('out/data.csv'); print(df.describe())"
sqlite3 out/data.db '.mode csv' '.import out/data.csv t' 'select count(*) from t'
```

## Convert documents

```bash
pandoc -f html -t gfm out/page.html -o out/page.md
```

Write results to `out/`, and list each output file in `RESULT.md`.
