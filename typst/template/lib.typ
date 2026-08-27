// Book template — page setup, chapter openings, figures, quotes, citations.
// Everything here is plain Typst; edit freely, there is no hidden package.

#let book(
  title: "",
  subtitle: none,
  author: "",
  date: none,
  // "pt" gives Portuguese hyphenation plus localised "Figura"/"Tabela"/
  // "Sumário" labels. Use "en" for an English book.
  lang: "pt",
  paper: "a4",
  // Print-ready by default: wider inside margin to survive the binding.
  // For a screen-only PDF, pass margin: (x: 2.5cm, y: 2.5cm).
  margin: (inside: 3cm, outside: 2cm, top: 2.5cm, bottom: 2.5cm),
  body,
) = {
  set document(title: title, author: author)
  set text(size: 11pt, lang: lang)
  set par(justify: true, leading: 0.78em, first-line-indent: (amount: 1.5em, all: true))
  set heading(numbering: "1.1")

  // ---- Cover -------------------------------------------------------------
  set page(paper: paper, margin: margin, numbering: none)
  v(1fr)
  align(center)[
    #text(size: 26pt, weight: "bold")[#title]
    #if subtitle != none [
      #v(0.6em)
      #text(size: 15pt, style: "italic")[#subtitle]
    ]
    #v(2.5em)
    #text(size: 13pt)[#author]
    #if date != none [
      #v(0.4em)
      #text(size: 11pt)[#date]
    ]
  ]
  v(1fr)
  pagebreak()

  // ---- Front matter: roman numerals, unnumbered outline -------------------
  set page(numbering: "i")
  counter(page).update(1)
  outline(depth: 2)
  pagebreak(weak: true)

  // ---- Body: arabic numerals, running header with the chapter name -------
  set page(
    numbering: "1",
    header: context {
      // Physical page numbers on both sides of the comparison: the page
      // counter was reset above, so `counter(page).get()` would be a
      // different scale from `location().page()` and never match.
      let page-no = here().page()
      let chapters = query(heading.where(level: 1))
      let current = chapters.rev().find(c => c.location().page() <= page-no)
      // Nothing before the first chapter, and nothing on a chapter opening.
      if current == none or current.location().page() == page-no { return }
      set text(size: 9pt)
      let no = counter(heading).at(current.location()).first()
      [#no #sym.dot.c #emph(current.body)]
      line(length: 100%, stroke: 0.4pt)
    },
  )
  counter(page).update(1)

  // `@rotulo` on a chapter should read "Capítulo 2", not the default "Seção 2".
  show heading.where(level: 1): set heading(
    supplement: if lang == "pt" { [Capítulo] } else { [Chapter] },
  )

  // Each level-1 heading starts a new page and reads "Capítulo N".
  show heading.where(level: 1): it => {
    pagebreak(weak: true)
    v(1.5cm)
    block(below: 1.2cm)[
      #set text(size: 11pt, weight: "regular")
      #if it.numbering != none [
        #upper(if lang == "pt" { "Capítulo" } else { "Chapter" })
        #counter(heading).display(it.numbering)
      ]
      #v(0.3em)
      #text(size: 24pt, weight: "bold")[#it.body]
    ]
  }

  // Figures: caption under the image, smaller and greyed.
  show figure.caption: it => text(size: 9.5pt, fill: rgb("#444"))[#it]

  // Block quotes: indented, ruled on the left, attribution on its own line.
  show quote.where(block: true): it => {
    set text(size: 10.5pt, style: "italic")
    block(
      inset: (left: 1.2em, y: 0.6em),
      stroke: (left: 2pt + rgb("#bbb")),
      width: 100%,
      it,
    )
  }

  body
}

// Bibliography section, kept here so chapters never repeat the styling.
// `style` takes a bundled CSL name or a path to your own .csl file.
#let references(path: "refs.bib", style: "ieee", title: "Referências") = {
  pagebreak(weak: true)
  bibliography(path, style: style, title: title)
}
