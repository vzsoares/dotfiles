# pi models and providers

Source: pi 1.6.0 docs, scraped 2026-11-20. Re-check with scripts/refresh.py.

## Picking a connection

| You have | Use |
|---|---|
| Subscription | `/login` (OAuth) |
| Provider API key | `/login` or the provider env var |
| Local GGUF | llama.cpp router (`/login llama.cpp`, `/llama`) |
| OpenAI/Anthropic/Google-compatible endpoint (Ollama, LM Studio, vLLM, SGLang, proxy) | `models.json` |
| Custom protocol or auth | provider extension |

- pi is multi-provider (unlike Claude Code / Codex, which are tied to one vendor by default).
- Catalog: bundled, overlaid with newer data from pi.dev; cached for offline use. Force refresh with
  `pi update --models`. `--offline`/`PI_OFFLINE` stops refreshes.
- `/model` lists only models whose provider has usable credentials; `Ctrl+S` there saves the default.
  `/thinking` + `Ctrl+S` saves the startup level. `Ctrl+P` cycles, scoped by `/scoped-models` or
  `enabledModels`. Sessions record model/thinking changes and restore them on resume without changing
  defaults.

Source: https://docs.pi.dev/latest/models

## Credential precedence

1. `--api-key` on the command line (runtime only)
2. stored credential in `~/.pi/agent/auth.json` (written by `/login` / `pi auth login`)
3. `apiKey` in `models.json`
4. provider env var or ambient cloud credentials (AWS, GCP ADC)

- Provider extensions may define their own auth. `/logout` only removes the `auth.json` entry; it does not
  unset env vars, edit `models.json`, or revoke the key upstream.
- Headless OAuth: paste the final redirect URL or code back into pi, or `pi auth login <p> --no-browser`.
- "Works in one shell only" usually means the key came from an env var, not `auth.json`.
- Secret-manager keys in `auth.json` (run once on first use, cached for the process; failure leaves the key
  unresolved until restart):

```
{ "anthropic": { "type": "api_key",
    "key": "!security find-generic-password -ws 'anthropic'" } }
```

- An `auth.json` credential may carry an `env` object whose values beat the process env for that provider
  (e.g. `CLOUDFLARE_ACCOUNT_ID`).

Source: https://docs.pi.dev/latest/models, https://docs.pi.dev/latest/providers

## Provider env vars

| Provider | Variable |
|---|---|
| Anthropic | `ANTHROPIC_API_KEY` (also `ANTHROPIC_OAUTH_TOKEN`, bearer `ANTHROPIC_AUTH_TOKEN`) |
| OpenAI | `OPENAI_API_KEY` |
| Google Gemini | `GEMINI_API_KEY` |
| OpenRouter | `OPENROUTER_API_KEY` |
| GitHub Copilot | `COPILOT_GITHUB_TOKEN` |
| DeepSeek / Mistral / Groq / Cerebras / xAI | `DEEPSEEK_API_KEY` / `MISTRAL_API_KEY` / `GROQ_API_KEY` / `CEREBRAS_API_KEY` / `XAI_API_KEY` |
| Vercel AI Gateway | `AI_GATEWAY_API_KEY` |
| Fireworks / Together / Baseten | `FIREWORKS_API_KEY` / `TOGETHER_API_KEY` / `BASETEN_API_KEY` |
| Hugging Face | `HF_TOKEN` |
| NVIDIA NIM | `NVIDIA_API_KEY` |
| ZAI (global / China) | `ZAI_API_KEY` / `ZAI_CODING_CN_API_KEY` |
| OpenCode Zen/Go | `OPENCODE_API_KEY` |
| Moonshot / Kimi | `MOONSHOT_API_KEY` / `KIMI_API_KEY` |
| MiniMax (global / CN) | `MINIMAX_API_KEY` / `MINIMAX_CN_API_KEY` |
| Radius (pi's own gateway) | `RADIUS_API_KEY` |
| TypeSafe (classifiers) | `TYPESAFE_API_KEY` |

Others in the docs: `ANT_LING_API_KEY`, `META_API_KEY`, `QWEN_TOKEN_PLAN_{TEAM,INDIVIDUAL}[_CN]_API_KEY`,
`XIAOMI_API_KEY`, `XIAOMI_TOKEN_PLAN_{CN,AMS,SGP}_API_KEY`.

- Anthropic workload identity (no key set): `ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`,
  `ANTHROPIC_IDENTITY_TOKEN_FILE` (+ optional `ANTHROPIC_SERVICE_ACCOUNT_ID`, `ANTHROPIC_WORKSPACE_ID`).

Source: https://docs.pi.dev/latest/providers

## Provider-specific setup

- **Azure**: provider ID `azure` (old name `azure-openai-responses`), e.g. `--model azure/gpt-5.4`.
  `AZURE_OPENAI_API_KEY` + `AZURE_OPENAI_BASE_URL` or `AZURE_OPENAI_RESOURCE_NAME`. Model ID is used as the
  deployment name; remap with `AZURE_OPENAI_DEPLOYMENT_NAME_MAP=model=deployment,...`.
  `AZURE_OPENAI_API_VERSION` (default `v1`). Extra Foundry models go under `azure` in `models.json` with
  `api: "openai-completions"` and a `baseUrl` (env vars take priority).
- **Bedrock**: `AWS_PROFILE`, or `AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY`[/`AWS_SESSION_TOKEN`], or
  `AWS_BEARER_TOKEN_BEDROCK`; region `AWS_REGION`/`AWS_DEFAULT_REGION`. ECS task creds and IRSA work.
- **Vertex AI**: `GOOGLE_CLOUD_API_KEY`, or ADC with `GOOGLE_CLOUD_PROJECT` (or `GCLOUD_PROJECT`) +
  `GOOGLE_CLOUD_LOCATION` after `gcloud auth application-default login`, or
  `GOOGLE_APPLICATION_CREDENTIALS`.
- **Cloudflare AI Gateway**: `CLOUDFLARE_API_KEY`, `CLOUDFLARE_ACCOUNT_ID`, `CLOUDFLARE_GATEWAY_ID`.
  **Workers AI**: `CLOUDFLARE_API_KEY`, `CLOUDFLARE_ACCOUNT_ID`.
- **OpenRouter**: with a key, the list is filtered to what that key may use (`/models/user`), refreshed at
  interactive startup, on `/model`, `/login`, `pi update --models`; cached list used in `-p`/RPC. Regional
  endpoint: set `providers.openrouter.baseUrl` (e.g. `https://us.openrouter.ai/api/v1`).
- **Radius**: Earendil's gateway (`/login radius`); adds an MCP server too. Early alpha.

Source: https://docs.pi.dev/latest/providers

## models.json: providers and models

Location: `~/.pi/agent/models.json` (agent dir only). Reloaded whenever `/model` opens.
Schema: `"$schema": "https://pi.dev/schemas/models.schema.json"`.

```
{ "providers": { "ollama": {
    "baseUrl": "http://localhost:11434/v1",
    "api": "openai-completions", "apiKey": "ollama",
    "models": [ { "id": "qwen2.5-coder:7b" } ] } } }
```

- A key matching a built-in provider ID modifies it; any other key creates a provider (needs `baseUrl` and
  an `api` at provider or model level). Setting only `baseUrl` on a built-in proxies it, keeping its models
  and auth.
- `api` values: `openai-completions`, `openai-responses`, `anthropic-messages`, `google-generative-ai`
  (also `azure-openai-responses`, `bedrock-converse-stream`, `mistral-conversations`, ...).
- Provider fields: `name`, `baseUrl`, `apiKey`, `api`, `oauth` (`"radius"`), `headers`, `compat`,
  `authHeader` (send key as Bearer, default false), `models`, `modelOverrides`.
- Value interpolation for `apiKey`/headers: `$NAME` / `${NAME}` env, literal, or `!command` (run on each
  request, not cached here -- unlike `auth.json`). `$$` and `$!` escape.
- Dummy `apiKey` is needed even for keyless local servers, else the model stays unavailable.
- Model fields: `id` (req), `name`, `api`, `baseUrl`, `reasoning` (false), `thinkingLevelMap` (null =
  unsupported level), `input` (`["text"]`, add `"image"`), `inputLimits`, `cost` {`input`, `output`,
  `cacheRead`, `cacheWrite`, `tiers[]` per M tokens}, `promptCache` {`short`,`long` seconds},
  `contextWindow` (128000), `maxTokens` (16384), `samplingParams`, `samplingParamsByThinkingLevel`,
  `headers`, `compat`.
- A `models` entry with an existing ID replaces that model. `modelOverrides` (keyed by built-in or
  extension model ID; unknown IDs ignored) patches metadata; object fields like `cost` merge per field.

Source: https://docs.pi.dev/latest/models-configuration, https://docs.pi.dev/latest/models

## Sampling, images, caching, compat

- `samplingParams` / `samplingParamsByThinkingLevel` (keys are pi levels `off..max`) only apply to
  `openai-completions`, `openai-responses`, `azure-openai-responses`. Merge order: model defaults, level
  override, request params; later wins per key.
- `inputLimits.images.resize` {`maxWidth`,`maxHeight`,`maxBytes` (base64),`jpegQuality`}; defaults
  2000x2000, 4.5 MiB, quality 80. Images are encoded once; switching models does not re-encode history.
- `promptCache` must be set for cache warming to apply (use the conservative end of published ranges).
- `compat` = only verified deviations; model-level beats provider-level. Notable keys:
  - openai-completions: `supportsDeveloperRole`, `maxTokensField`, `thinkingFormat`
    (`openai|openrouter|deepseek|qwen|qwen-chat-template|chat-template|zai|...`), `chatTemplateKwargs`,
    `thinkingTokenBudgetField` (`thinking_budget_tokens` for llama.cpp), `requiresToolsWithToolHistory`
    (old LiteLLM), `cacheControlFormat: "anthropic"`, `openRouterRouting`.
  - openai-responses: `supportsLongCacheRetention`, `supportsMaxOutputTokens` (some Codex gateways need
    false), `promptCacheKeyMode`.
  - anthropic-messages: `supportsCacheControlOnTools` (false for Fireworks), `supportsTemperature`,
    `forceAdaptiveThinking`, `supportsMessageThreads`.

Source: https://docs.pi.dev/latest/models-configuration, https://docs.pi.dev/latest/models

## Classifier and image models

- Not chat models; they never appear in `/model`. Reached via the `codemode` tool (enable
  `"defaultTools": ["+codemode"]`): `models.getAvailableOfType("classifier"|"image")`,
  `models.classify(model, {state, questions})`, `models.generateImages(model, {input})`.
  Extensions use `ctx.modelRegistry.classify()` / `generateImages()`.
- Built-in classifiers include `typesafe/jev-latest`, Cloudflare Clef via Workers AI/OpenRouter,
  `openai/gpt-6-luna` (needs a real `OPENAI_API_KEY`; hidden while `openai` is logged in via ChatGPT
  OAuth), `github-copilot/microsoft-decision-1`. Image models: OpenRouter's (e.g.
  `google/gemini-2.5-flash-image`). Usage is billed into the codemode tool result.

Source: https://docs.pi.dev/latest/models

## Local models: llama.cpp router

- Install `curl -LsSf https://llama.app/install.sh | sh`. Run the router WITHOUT `-m/--model/-hf`
  (those start single-model mode): `llama serve --no-models-autoload -ngl all -c 32768`.
- Put multimodal/sharded GGUFs in their own subdirectories; restart router after adding files.
- In pi: `/login llama.cpp` (default URL `http://127.0.0.1:9931`) or env `LLAMA_BASE_URL`,
  `LLAMA_API_KEY`. With `--no-models-autoload`, load via `/llama` first, then select in `/model`.
- `/llama` loads/unloads/downloads (HF search uses `HF_TOKEN` or the HF token files; the router process
  also needs `HF_TOKEN` for gated repos). pi never deletes model files.
- Chat models double as classifiers (`llama-cpp-classify`, next-token label probabilities); decision
  models use `/v1/systemone` (`typesafe-system-one`). Hybrid models may need
  `--ctx-checkpoints 32 --checkpoint-min-step 0`.
- Health: `curl http://127.0.0.1:9931/health` and `/models`. Disable with
  `"extensions": ["-builtin:llama.cpp"]`.
- NVIDIA RTX recipes: convert the `llama-server` command to an `.ini` preset (drop dashes, flags ->
  `= true`, `-hf`->`hf-repo`, `-hff`->`hf-file`, `-hfrd`->`hf-repo-draft`, omit host/port, add
  `load-on-startup = true`), then `llama serve --models-preset <file>.ini --host 127.0.0.1`. The section
  name becomes the pi model ID. Use `curl.exe` in PowerShell.

Source: https://docs.pi.dev/latest/llama-cpp, https://docs.pi.dev/latest/nvidia-rtx-llama-cpp

## Custom providers and virtual models (extensions)

- Prefer `models.json`; write a provider extension only for custom auth, live discovery, or an unsupported
  wire protocol. Extensions are trusted code that sees prompts and credentials.
- `pi.registerProvider("anthropic", { baseUrl, headers })` redirects a built-in while keeping its catalog;
  passing `models` replaces the provider's models across chat/image/classifier. `pi.unregisterProvider()`
  restores. OAuth providers show up in `/login` and store tokens in `~/.pi/agent/auth.json`.
- Dynamic catalogs: `refreshModels`. Reuse built-in stream APIs (Anthropic, OpenAI Completions/Responses,
  Google, Vertex, Azure, Mistral, Bedrock) rather than writing `streamSimple`.
- Virtual models: `pi.registerVirtualModel({ provider, id, name, thinkingLevels, route(request, ctx) })`
  returns `{ model, thinkingLevel, state? }` per request. They appear in `/model`, `--model`, settings.
  `request.reason` is `user`/`continuation`/`retry`/`direct`; return `request.previous` on continuations to
  keep the prompt cache. `PI_MODEL` shows the virtual selection; assistant messages record the physical
  model. Cannot route to another virtual model.

Source: https://docs.pi.dev/latest/custom-provider, https://docs.pi.dev/latest/virtual-models
