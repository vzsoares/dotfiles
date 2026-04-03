---
name: yq
description: Manage YAML files using yq (kislyuk/yq, Python jq-wrapper). Read, update, filter, and transform YAML data. Includes task board management with backlog/current/done workflows.
argument-hint: [action] [file] [details]
allowed-tools: Read, Bash, Glob, Grep
---

# yq YAML Manager Skill

You manage YAML files using **yq** (kislyuk/yq — the Python wrapper around jq). All filters use **jq syntax**.

## Important: This is kislyuk/yq, NOT mikefarah/yq

- Installed at: `~/.local/bin/yq` (v3.4.3)
- Filters are **pure jq syntax**
- YAML output requires `-y` or `-Y` flag
- In-place editing requires `-yi` or `-Yi` (NOT just `-i`, which would write JSON)

## Core flags

| Flag | Meaning |
|------|---------|
| `yq '.filter' file.yaml` | YAML in, **JSON** out |
| `yq -y '.filter' file.yaml` | YAML in, **YAML** out |
| `yq -Y '.filter' file.yaml` | YAML in, **YAML** out (wider lines) |
| `yq -yi '.filter' file.yaml` | In-place edit, YAML output |
| `yq -r '.filter' file.yaml` | Raw string output (no quotes) |
| `yq --arg key val` | Pass string variable into filter |
| `yq --argjson key val` | Pass JSON variable into filter |

## Common operations

### Read a path
```bash
yq -y '.some.path' file.yaml
yq -r '.some.path[0].name' file.yaml
```

### Set a value
```bash
yq -yi '.some.path = "value"' file.yaml
yq -yi '.some.list += ["new item"]' file.yaml
```

### Filter arrays
```bash
yq -y '[.items[] | select(.status == "active")]' file.yaml
yq -y '[.items[] | select(.name | test("pattern"))]' file.yaml
```

### Delete from arrays
```bash
yq -yi '.items |= map(select(.id != 3))' file.yaml
```

### Update items in arrays
```bash
yq -yi '(.items[] | select(.id == 1)).title = "new title"' file.yaml
```

### Guard against null arrays
```bash
yq -yi '.items = (.items // [] | map(select(.id != 1)))' file.yaml
```

---

# Task Board Management

When the user asks about tasks, backlog, current work, or done items, operate on a **tasks.yaml** file in the current working directory (or the path they specify).

## Task file schema

```yaml
backlog:
  - id: 1
    title: "task description"
    tags: [optional, labels]
    notes: "optional details"
current:
  - id: 2
    title: "what I'm working on"
done:
  - id: 3
    title: "completed task"
```

All three sections (`backlog`, `current`, `done`) are arrays. Each task must have a unique `id` (integer) and a `title` (string). Other fields are optional.

## Task commands

When the user says... do this:

### "show tasks" / "list tasks" / "show board"
Show all three sections formatted as a board:

```bash
echo "=== CURRENT ===" && yq -r '(.current // [])[] | "  [\(.id)] \(.title)"' tasks.yaml 2>/dev/null; \
echo "=== BACKLOG ===" && yq -r '(.backlog // [])[] | "  [\(.id)] \(.title)"' tasks.yaml 2>/dev/null; \
echo "=== DONE ===" && yq -r '(.done // [])[] | "  [\(.id)] \(.title)"' tasks.yaml 2>/dev/null
```

### "show backlog" / "show current" / "show done"
Show only the requested section:

```bash
yq -r '(.<section> // [])[] | "[\(.id)] \(.title)"' tasks.yaml
```

### "add task <title>" / "add to backlog <title>"
Add a new task to backlog with auto-incremented id:

```bash
yq -yi --arg t "TITLE" '
  ([.backlog, .current, .done | (.// [])[]?.id] | if length == 0 then 0 else max end) + 1 as $nid |
  .backlog = (.backlog // []) + [{"id": $nid, "title": $t}]
' tasks.yaml
```

### "start <id>" / "move <id> to current"
Move a task from backlog to current:

```bash
yq -yi --argjson id ID '
  ((.backlog // [])[] | select(.id == $id)) as $task |
  .current = (.current // []) + [$task] |
  .backlog = [(.backlog // [])[] | select(.id != $id)]
' tasks.yaml
```

### "done <id>" / "finish <id>" / "complete <id>"
Move a task from current to done:

```bash
yq -yi --argjson id ID '
  ((.current // [])[] | select(.id == $id)) as $task |
  .done = (.done // []) + [$task] |
  .current = [(.current // [])[] | select(.id != $id)]
' tasks.yaml
```

### "move <id> to <section>"
Generic move — find the task in any section and move it:

```bash
yq -yi --argjson id ID --arg to "SECTION" '
  ([.backlog, .current, .done | (.// [])[] | select(.id == $id)] | first) as $task |
  .[$to] = (.[$to] // []) + [$task] |
  .backlog = [(.backlog // [])[] | select(.id != $id)] |
  .current = [(.current // [])[] | select(.id != $id)] |
  .done = [(.done // [])[] | select(.id != $id)]
' tasks.yaml
```

### "delete <id>" / "remove <id>"
Remove a task from all sections:

```bash
yq -yi --argjson id ID '
  .backlog = [(.backlog // [])[] | select(.id != $id)] |
  .current = [(.current // [])[] | select(.id != $id)] |
  .done = [(.done // [])[] | select(.id != $id)]
' tasks.yaml
```

### "edit <id> title <new title>"
Update a task title:

```bash
yq -yi --argjson id ID --arg t "NEW TITLE" '
  ((.backlog // [])[] | select(.id == $id)).title = $t //
  ((.current // [])[] | select(.id == $id)).title = $t //
  ((.done // [])[] | select(.id == $id)).title = $t
' tasks.yaml
```

### "init tasks" / create a new task file
```bash
echo 'backlog: []\ncurrent: []\ndone: []' > tasks.yaml
```

## Rules

1. **Always use `-yi`** for in-place edits — never `-i` alone (that writes JSON).
2. **Always guard with `// []`** for sections that might be null or missing.
3. **Show the result** after any mutation — run the "show board" command so the user sees the new state.
4. **Use `--arg` and `--argjson`** for dynamic values — never interpolate shell variables into jq filters.
5. If `tasks.yaml` doesn't exist and the user asks to add a task, create it first with `init tasks`.
6. The task file path defaults to `tasks.yaml` in the current directory unless the user specifies another path.
