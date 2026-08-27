= Fundamentos <sec-fundamentos>

Cada capítulo é um arquivo em `chapters/`, incluído por `book.typ` com
`#include`. Abrir um capítulo sozinho no nvim continua funcionando: o LSP
entende o arquivo, e `:TypstPin book.typ` garante que o PDF gerado seja o
livro inteiro.

== Estrutura de seções

Os níveis seguem a contagem de `=`:

- `=` capítulo (abre página nova, numerado como "Capítulo N")
- `==` seção
- `===` subseção

=== Uma subseção

Notas de rodapé são inline#footnote[Como esta, numerada automaticamente.] e
não exigem pacote nenhum.

== Código e matemática

Blocos de código preservam realce por linguagem:

```python
def hifenizar(texto: str) -> list[str]:
    return texto.split("-")
```

E fórmulas usam a sintaxe própria do Typst, sem `$$` do LaTeX:

$ sum_(i=1)^n i = (n (n + 1)) / 2 $
