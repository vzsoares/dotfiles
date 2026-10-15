---
name: cms-faq
description: Read and edit the Approva site FAQ through the task-flow CMS API (/cms/api/faq) — list FAQ lists, read a draft, add/update/remove questions, publish, unpublish. Use when the user says "FAQ", "perguntas comuns", "perguntas caixa", "CMS", "publicar o FAQ", or wants a site answer changed.
argument-hint: <list|show|edit|publish|unpublish> [slug] [dev|prod]
allowed-tools: Read Bash
---

# cms-faq — the site FAQ, by API

The landing's FAQ lives in task-flow's CMS (app `cms`, wiki `docs/wiki/cms_conteudo.md`).
This skill edits it through `/cms/api/faq`, the same draft → publish cycle the backoffice
uses. **Only FAQ** is editable by API; heroes and journeys are backoffice-only.

## Config — private, token in SSM

Base URL and the SSM parameter name live in the private vault: read `config/cms-faq.json`
under the `vault` path in `~/.claude/second-brain.json`. Shape:

```json
{ "dev":  { "base_url": "https://…", "aws_profile": "…", "token_ssm": "/dev/…" },
  "prod": { "base_url": "https://…", "aws_profile": "…", "token_ssm": "/prod/…" } }
```

The token itself is a SecureString in SSM (a task-flow `ChaveApi` with scope `cms.editar`,
minted by `uv run manage.py criar_chave_api --nome llm-cms --escopos cms.editar --aplicar`).
Never print it, never paste it into chat, never write it to disk. Resolve it inside the
same shell command that uses it:

```bash
VAULT=$(sed -n 's/.*"vault": *"\([^"]*\)".*/\1/p' ~/.claude/second-brain.json)
CFG="$VAULT/config/cms-faq.json"; STAGE=dev
BASE=$(jq -r ".$STAGE.base_url" "$CFG")
TOKEN=$(AWS_PROFILE=$(jq -r ".$STAGE.aws_profile" "$CFG") aws ssm get-parameter \
  --name "$(jq -r ".$STAGE.token_ssm" "$CFG")" --with-decryption \
  --query Parameter.Value --output text)
echo "${#TOKEN} chars"   # 52 expected; 0 means the SSO session expired
curl -sS -H "Authorization: Bearer $TOKEN" "$BASE/cms/api/faq?produto=marketplace"
```

Default stage is **dev**. Prod publishes change the live site: use `prod` only when the
user names it, and confirm the diff before `publicar`.

## Model

- A **list** is `(produto, parceiros, slug)`. No parceiros = base (all partners);
  parceiros set = overrides the base for those slugs only. Slugs in use:
  `perguntas-comuns` (shared modal fallback), `perguntas-comuns-home`,
  `perguntas-caixa` (caixa/tsr/arrematador + credit form), `perguntas-direcional`.
- Items are `{ "title", "desc": ["paragraph", …], "secao"? }` — identical to the public
  payload of `/cms/api/conteudo`. `desc` paragraphs accept inline HTML from the allowlist
  only: `strong em ul ol li a[href] br p span small`. Anything else → 400 with
  `erros[{item, campo, mensagem}]` and nothing is written.
- `PUT` **replaces the whole item list**. Always GET first, edit the array, PUT it back.
- Edits land in the **draft**; the site only changes after `publicar`. `situacao` is
  `nunca_publicado` | `pendente` (draft differs from live) | `publicado`.

## Routes (all need `Authorization: Bearer <token>`, scope `cms.editar`)

| Call | Does |
| --- | --- |
| `GET  /cms/api/faq?produto=marketplace` | lists: `id slug parceiros situacao itens` |
| `POST /cms/api/faq` `{slug, nome?, parceiros?, itens}` | create a list |
| `GET  /cms/api/faq/<id>` | draft `itens` (with `id`/`ordem`) + `noAr` (live version) |
| `PUT  /cms/api/faq/<id>` `{itens, nome?, notas?, parceiros?}` | replace the draft |
| `POST /cms/api/faq/<id>/publicar[?purgar=0]` | new version + site cache purge |
| `POST /cms/api/faq/<id>/despublicar` | off air, site falls back to `@/content` |

## Workflow

1. `list` — GET the lists, show `slug → id, situacao, itens` as a table.
2. `show <slug>` — GET the list, print questions numbered; mark items that differ from
   `noAr`.
3. `edit <slug>` — GET, apply the user's change to the array in a temp JSON file under
   `$CLAUDE_JOB_DIR/tmp` (or `mktemp`), `curl -X PUT --data-binary @file`, then show the
   resulting draft. Keep the user's wording; do not rewrite other items.
4. `publish <slug>` — show the draft vs `noAr` diff in one line per changed question, then
   `POST …/publicar`. Report `versao` and whether `purgas[].ok`.
5. A question that must exist in several lists (e.g. `perguntas-comuns` and
   `perguntas-caixa`) is one edit per list, then one publish per list.

Source of truth for *new* copy is the landing repo `src/content/faq.ts`
(`PERGUNTAS_COMUNS`, `PERGUNTAS_CAIXA`, …): when the user adds a question there, mirror
it here so the published CMS version does not hide it.
