// Entry file. Compile this one, never a chapter on its own:
//   typst watch book.typ        # live rebuild while you write
//   typst compile book.typ      # one-off
// In nvim, :TypstPin here once per session so tinymist exports the whole
// book on every save, wherever the cursor is.

#import "lib.typ": book, references

#show: book.with(
  title: "Título do Livro",
  subtitle: "Um subtítulo opcional",
  author: "Vinicius Zenha",
  date: "2026",
  lang: "pt",
)

#include "chapters/01-introducao.typ"
#include "chapters/02-fundamentos.typ"

#references(style: "ieee")
