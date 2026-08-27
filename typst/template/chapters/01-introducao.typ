= Introdução

Este capítulo existe para mostrar cada recurso que um livro precisa. Apague o
conteúdo e mantenha a estrutura.

Um parágrafo comum já sai justificado e com recuo de primeira linha, definidos
uma única vez em `lib.typ`.

== Citações bibliográficas

A forma curta usa `@chave` e vira uma citação numérica ou autor-data conforme o
estilo: a hifenização em Typst segue o algoritmo de Knuth e Plass @knuth1984.
Para citar o autor dentro da frase, use a forma prosa:
#cite(<lamport1994>, form: "prose") descreve o mesmo problema em LaTeX.

Todas as entradas vêm de `refs.bib` — o mesmo arquivo que o Zotero
(Better BibTeX) exporta, sem conversão.

== Trechos citados

#quote(block: true, attribution: [Donald Knuth @knuth1984])[
  Programs are meant to be read by humans and only incidentally for computers
  to execute.
]

Citação curta no meio do texto também funciona: #quote[a forma inline não quebra
o parágrafo].

== Imagens e referências cruzadas

A @fig-exemplo mostra uma figura numerada automaticamente. No nvim,
`<leader>ip` cola a imagem da área de transferência já dentro de um `#figure`.

Atenção ao caminho: o Typst resolve caminhos relativos a partir do arquivo que
os escreve, então um capítulo em `chapters/` que pedisse `fig/algo.png`
procuraria em `chapters/fig/`. Comece com `/` para falar da raiz do projeto —
`image("/fig/algo.png")` funciona igual em qualquer capítulo.

#figure(
  rect(width: 60%, height: 3cm, fill: luma(240), stroke: 0.5pt)[
    #align(center + horizon)[troque por #raw("image(\"/fig/algo.png\")")]
  ],
  // Só aqui: sem uma imagem de verdade dentro, o Typst chamaria isto de
  // "Listagem". Uma `#figure(image(...))` já vira "Figura" sozinha.
  kind: image,
  caption: [Uma figura de exemplo, referenciável por rótulo.],
) <fig-exemplo>

Rótulos servem para qualquer coisa: veja @sec-fundamentos para o próximo
capítulo, ou a @tab-exemplo abaixo.

#figure(
  table(
    columns: 3,
    table.header([*Formato*], [*Compila em*], [*Precisa de LaTeX*]),
    [Typst], [ms], [não],
    [Markdown + Pandoc], [s], [depende do engine],
    [LaTeX], [s], [sim],
  ),
  caption: [Comparação dos formatos avaliados.],
) <tab-exemplo>
