# Typst

Long-form writing setup — books, TCC, reports — with references, images,
citations and quotes. Chosen over LaTeX because it is a single ~30 MB binary
(`mise install typst`) instead of a TeX distribution, compiles in milliseconds,
and has figures, cross-references, bibliographies and block quotes as language
primitives rather than packages.

## Starting a book

```sh
cp -r ~/code/personal/dotfiles/typst/template ~/code/personal/my-book
cd ~/code/personal/my-book
just            # typst watch — rebuilds book.pdf on every save
```

`template/` compiles as-is; it is a working example of every feature, meant to
be gutted. Layout lives in `lib.typ`, prose in `chapters/`.

```
book.typ            entry file — metadata + #include of each chapter
lib.typ             the template: page setup, chapter openings, quotes, figures
chapters/*.typ      one file per chapter
refs.bib            BibTeX, exported straight from Zotero (Better BibTeX)
fig/                images
```

## Editor

`nvim` is wired up in `../nvim`:

| | |
|---|---|
| `tinymist` LSP | completion, diagnostics, goto-definition on labels and citations |
| `<leader>f` | format (typstyle, via the LSP fallback in conform) |
| save | exports the PDF (`exportPdf = "onSave"`) |
| `:TypstPin` | pin `book.typ` as the entry so saving *any* chapter rebuilds the whole book |
| `:TypstUnpin` | back to compiling the focused file |
| `:TypstOpen` | open the exported PDF in the OS viewer |
| `<leader>ip` | paste a clipboard image as a `#figure` (img-clip) |

Run `:TypstPin` once per session with `book.typ` open — without it tinymist
compiles whatever file is focused and a chapter exports on its own.

Alacritty renders no inline images, so keep the PDF open beside nvim (`just`
in a tmux pane, or `:TypstOpen`) rather than expecting a preview in the buffer.

## Gotchas

- **Paths are relative to the file that writes them.** A chapter in
  `chapters/` asking for `fig/x.png` looks in `chapters/fig/`. Start the path
  with `/` — `image("/fig/x.png")` — to mean the project root.
- **Grammar checking works here.** LTeX+ parses Typst markup, so `ltex_plus`
  attaches to `.typ` and `<leader>ts` / `<leader>tl` behave as in markdown.
- **Bibliography style** is the `style:` argument of `#references()` in
  `book.typ`. Bundled CSL names work (`"ieee"`, `"chicago-author-date"`,
  `"mla"`, …) and so does a path to any `.csl` file — drop an ABNT `.csl` next
  to `refs.bib` and point at it for a Brazilian thesis.

## Reference

- Docs: <https://typst.app/docs/>
- Bundled citation styles: <https://typst.app/docs/reference/model/bibliography/#parameters-style>
- Packages (templates, charts, glossaries): <https://typst.app/universe/>
