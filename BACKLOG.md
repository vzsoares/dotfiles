# Backlog

Things worth doing, not yet done. Drop an entry when it ships.

---

## 1. Grammar checking for formats LTeX+ does not parse

**Status:** idea — no format currently blocked, so this waits for a real trigger.

LTeX+ covers LaTeX, Markdown, MDX, Typst, AsciiDoc, Org, reStructuredText,
BibTeX, ConTeXt, Quarto, R Sweave, HTML/XHTML and gitcommit. Everything I write
in today is on that list (Typst included — checked against
`nvim-lspconfig/lsp/ltex_plus.lua`, which has `typst` in both `filetypes` and
`settings.ltex.enabled`). This task only starts mattering when I adopt a format
that is *not* covered.

Three ways to fix that when it happens, cheapest first. Do them in order and
stop as soon as one is good enough — the plugin is the expensive option.

### Tier 0 — alias the filetype (3 lines, no plugin)

`ltex_plus` resolves its language id through a `get_language_id` hook, so an
unknown filetype can be told to check as a known one. In the existing
`vim.lsp.config("ltex_plus", …)` block in `nvim/lua/plugins/code.lua`:

```lua
get_language_id = function(_, ft)
  local alias = { myformat = "markdown", scratch = "plaintext" }
  return alias[ft] or ft
end,
```

Real grammar checking immediately; the cost is false positives wherever the
aliased parser fails to recognise markup. For anything markdown-shaped this is
80% of the value for 1% of the work. **Always try this first.**

### Tier 1 — the plugin (~200-300 lines of Lua)

Only if aliasing produces too much noise *and* upstream will not take a parser.

Shape:

1. Walk the buffer with treesitter; a per-format query says which node types are
   prose.
2. Replace every markup/code node with a dummy string **of the same length**, so
   offsets still map back to buffer positions. This masking is the whole point
   of the plugin and the only hard part.
3. Send the masked text to a local LanguageTool server, or to `harper-ls`
   (Rust, much faster; both are in the mason registry).
4. Publish results into a dedicated `vim.diagnostic` namespace.

Half of it is already written and worth reading first:
`~/.local/share/nvim/lazy/nvim-lint/lua/lint/linters/languagetool.lua` does the
LanguageTool-JSON-to-diagnostics conversion. It is not a substitute as-is — it
pipes the raw buffer with no masking, so every `#figure(...)` and `@cite` comes
back flagged as a grammar error.

Cost to accept: a masking query per format, maintained alone, plus a second
diagnostic pipeline living next to LTeX.

### Tier 2 — upstream a parser into LTeX+

The real fix, and smaller than it sounds. LTeX+ keeps one package per format
under `ltexls/src/main/kotlin/org/bsplines/ltexls/parsing/`. All of Typst
support is three Kotlin files, ~16 KB total (~500 lines):

| file | size |
|---|---|
| `TypstAnnotatedTextBuilder.kt` | 9.0 KB |
| `TypstModeHandler.kt` | 5.3 KB |
| `TypstFragmentizer.kt` | 2.2 KB |

Same idea as tier 1 — `CodeAnnotatedTextBuilder` walks the markup and emits text
plus dummy tokens. `CharacterBasedCodeAnnotatedTextBuilder` is the base class for
simpler formats, and `parsing/program/` already handles source-code comments
generically, so it is rarely a from-scratch job. Kotlin plus review latency, but
then everyone gets it, `ltex.enabled` picks it up, and there is nothing of mine
left to maintain.

**Done when:** grammar diagnostics appear in the target format with no
markup-induced false positives, and `<leader>ts` / `<leader>tl` toggle them
together with everything else (see `nvim/lua/config/spell.lua`).

---

## 2. AI suggestions on demand — kill the inline ghost text

**Status:** wanted.

Copilot currently writes itself into the buffer unasked. In
`nvim/lua/plugins/ai.lua` the `copilot.lua` spec sets `suggestion.enabled = true`
with `auto_trigger = true`, so ghost text appears as I type and `<M-Tab>` accepts
it. I want the opposite default: **nothing inline, ever**, and an explicit key
that goes and asks for a suggestion when I want one.

### Already true today

`panel` ships **enabled** in copilot.lua, so `<M-CR>` opens it right now — the
default keymaps are `open = <M-CR>`, and inside the panel `<CR>` accepts,
`[[` / `]]` jump between suggestions and `gr` refreshes, in a bottom split at
0.4 of the window. Nothing in `alacritty/`, `tmux/` or `i3/config` binds
Alt+Enter, so the default is free. **The trigger I want already exists; what is
missing is turning the inline half off.**

### Work

- `suggestion.enabled = false` in the `copilot.lua` opts
  (`nvim/lua/plugins/ai.lua`). That alone removes the ghost text and makes the
  whole `suggestion.keymap` table dead config — strip it in the same pass,
  including `accept = "<M-Tab>"`.
- Write the `panel` block out explicitly rather than leaning on the defaults, so
  the config states what the trigger is instead of hiding it.
- `nvim/lua/config/help.lua` still advertises `{ "<M-Tab>", "Accept Copilot
  suggestion" }` under "AI & Misc". Replace it with `<M-CR>` and the in-panel
  keys. Note that the panel's `gr` (refresh) shadows the global `gr` (LSP
  references) while the panel is focused — buffer-local, harmless, worth one
  line on the cheatsheet so it is not a surprise.

### Alternative, if the split turns out to be too heavy

`copilot-cmp` feeds suggestions into `nvim-cmp` as just another source next to
LSP/luasnip/buffer. Also non-inline, and reuses the completion UI already in
`nvim/lua/plugins/code.lua` — but it mixes AI output into the same menu as LSP
completions, which is its own kind of noise. New dependency. Only if the panel
annoys me in practice.

**Done when:** typing in a buffer produces no AI text of any kind, `<M-CR>`
brings suggestions up in a window I asked for, and the cheatsheet says so.
