# Security, trust and isolation

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Threat model in one paragraph

pi has no built-in sandbox and no per-tool-call approval prompt. It reads,
writes and executes with the OS permissions of the user running it;
extensions, package installers, LSPs and child processes do too. Repo files,
command output and model replies can prompt-inject it. Watching the
transcript, project trust and diff review are not security boundaries; real
protection comes from limiting files, credentials, processes and network
(container/VM/sandbox). The working directory only steers discovery and tool
defaults; it does not confine path access.

Difference from Claude Code / Codex: pi does not ask before each tool call
and has no built-in permission modes; control comes from OS isolation plus
optional extension gates on the `tool_call` event.

Source: https://docs.pi.dev/latest/security, https://docs.pi.dev/latest/how-pi-works

## Isolation options

| How pi runs | What stays protected |
|-------------|---------------------|
| Directly as your user | only what that user can't reach (a dedicated user narrows it) |
| Whole process inside container/VM/sandbox | host files/processes not exposed; usually strongest practical choice |
| Host pi, built-in tools routed into an isolated backend | host is protected only from those tools; pi and other extensions still on host |

Expose only needed files and services, prefer short-lived scoped
credentials, restrict network when not needed.

Source: https://docs.pi.dev/latest/security

## Project trust: what it gates

Trust is needed when the cwd contains any of:

- `.pi/settings.json`, `.pi/mcp.json`
- `.pi/extensions`, `.pi/skills`, `.pi/prompts`, `.pi/themes`
- `.pi/SYSTEM.md`, `.pi/APPEND_SYSTEM.md`
- `.agents/skills` in the cwd or any ancestor

An empty `.pi/` directory does not trigger it.

Granting trust loads project settings, project MCP servers, project
extensions/skills/prompts/themes/system-prompt files, project-local and
project-package extensions, and installs missing project packages.
Declining skips all of that.

NOT gated by trust:

- Context files `AGENTS.override.md`, `AGENTS.md`, `CLAUDE.md` always load
  (disable with `-nc` / `--no-context-files`). Treat them as untrusted input.
- The project `sessionDir` setting is read before trust is resolved.
- Trust never restricts what tools can touch at runtime.

Source: https://docs.pi.dev/latest/security, https://docs.pi.dev/latest/cli

## Project trust: how the decision is made

Order:

1. CLI: `-a` / `--approve` (trust for this process) or `-na` /
   `--no-approve` (ignore project resources for this process).
2. User-level or CLI-loaded extensions handling the `project_trust` event;
   the first yes/no wins.
3. Saved decision for the cwd or nearest parent, stored in
   `~/.pi/agent/trust.json` (canonical paths). Save one with `/trust`.
4. Global setting `defaultProjectTrust`: `"ask"` (default), `"always"`,
   `"never"`.

Headless gotcha: print (`-p`), JSON and RPC modes can't prompt. Without a
CLI flag, extension or saved decision, only `defaultProjectTrust: "always"`
loads project resources; `"ask"` and `"never"` silently skip them. So
automated runs relying on `.pi/mcp.json` or project skills need `--approve`
(or a saved `/trust`).

Source: https://docs.pi.dev/latest/security

## Startup order (how pi works)

1. Resolve project trust.
2. Load project settings/resources (if trusted).
3. Load context files (agent dir, then cwd and parents).
4. Build the request: system prompt (base + context files + `SYSTEM.md` /
   `APPEND_SYSTEM.md`), active branch history, tool definitions, skill
   names/descriptions (full skill text loaded on demand).

Agent loop: user message joins the active branch -> provider streams a reply
with text/tool calls -> pi runs each tool and records results (one turn) ->
repeats while tool results or queued messages need a reply. Steering
messages land after the current assistant turn; follow-ups after all pending
work; abort returns queued messages to the editor.

Interfaces sharing this core: interactive TUI, print mode, JSON mode (events
as JSONL), RPC mode (JSONL commands on stdin), TypeScript SDK. Extensions are
TypeScript modules running in the pi process.

Source: https://docs.pi.dev/latest/how-pi-works

## Plain Docker recipe

Image essentials: `node:24-bookworm-slim`, `bash ca-certificates git
ripgrep`, `npm install -g --ignore-scripts @earendil-works/pi-coding-agent`,
`WORKDIR /workspace`, `ENTRYPOINT ["pi"]`.

```
docker build -t pi-sandbox -f Dockerfile.pi .
docker run --rm -it \
  -e ANTHROPIC_API_KEY \
  -v "$PWD:/workspace" \
  -v pi-agent-home:/root/.pi/agent \
  pi-sandbox
```

- Use a named volume for `/root/.pi/agent`; do NOT bind-mount the host
  `~/.pi/agent` (exposes credentials, settings, extensions, sessions).
- Verify with `!pwd` inside pi -> `/workspace`. The bind mount writes
  through to the host; use `:ro` or copy in/out if that is unwanted.
- Env vars passed in are visible to every process in the container; network
  access can exfiltrate.

Source: https://docs.pi.dev/latest/containerization

## Other isolation methods

- Docker Sandboxes (`sbx`): whole pi process in a managed sandbox; a proxy
  keeps the real provider key on the host and substitutes it per request.
  `sbx run --kit "docker.io/sbx/pi-kit:latest" pi`;
  non-interactive: `sbx exec <name> -- pi -p "..."`. Set credentials with
  `sbx secret set anthropic` (API key) or `sbx secret set-custom` for an
  `ANTHROPIC_OAUTH_TOKEN` from `claude setup-token`. Never `/login` inside
  the sandbox (writes a real credential in).
- NVIDIA OpenShell: policy-driven local/remote sandboxes
  (`openshell gateway add|select`,
  `openshell sandbox create --name pi-sandbox --from pi -- pi`). Remote
  gateways don't bind-mount; use `openshell sandbox upload/download`.
  Inference routing can keep model keys outside.
- Gondolin extension: pi stays on host, built-in tools (`read`, `write`,
  `edit`, `bash`, `grep`, `find`, `ls`) and `!` commands run in a local
  Linux micro-VM with cwd at `/workspace`. Commands inherit host env vars,
  so env-supplied keys leak in; not a credential boundary. Needs Node 23.6+
  and QEMU. Run with `pi -e ~/.pi/agent/extensions/gondolin`.

Where extensions run follows where pi runs: inside for whole-process
isolation, on the host for tool-only isolation.

Source: https://docs.pi.dev/latest/containerization

## Hygiene checklist

- Snapshot / commit before large changes; review diffs before applying
  elsewhere.
- Review third-party extensions and packages first (they execute in-process).
- Review unfamiliar `.pi/` and `.agents/skills` before `/trust`.
- Review sessions before `/export` or `/share` (may contain secrets).
- Security reports go through the repo's Security Policy, not public issues.
  Prompt injection, lack of sandbox and user-installed extension behaviour
  are generally out of scope.

Source: https://docs.pi.dev/latest/security
