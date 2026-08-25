-- Corretor ortográfico (pt-BR) + gramática.
--
-- Dictionary: nvim/spell/pt.utf-8.spl — the VERO dictionary, which self-reports
-- as "NAME Brazilian Portuguese". Note nvim's built-in downloader CANNOT fetch
-- it: it asks the mirror for `<spelllang>.utf-8.spl`, so `pt_br` becomes a 404.
-- The file that exists upstream is `pt.utf-8.spl`, and Vim strips the region
-- when resolving 'spelllang', so `pt_br` resolves to it once the file is there.
-- :SpellFetchPtBr re-downloads it (e.g. on a fresh machine).

local M = {}

local SPELL_URL = "https://ftp.nluug.nl/pub/vim/runtime/spell/pt.utf-8.spl"
local SPELL_DIR = vim.fn.stdpath("config") .. "/spell"

-- Cycled by <leader>tl. Each mode drives BOTH engines: the built-in speller
-- ('spelllang') and the LTeX+ grammar server (ltex.language), so they never
-- disagree about what language you're writing.
--
-- "both" is the default: 'spelllang' accepts a word valid in either language,
-- which is what you want when switching between pt and en without ceremony.
-- Real typos are still caught -- "recieve"/"teh" fail in both dictionaries.
-- Its ltex code is "auto"; LTeX's bundled checker has no Ngram data, so
-- detection is "less accurate" per upstream. Pin pt/en explicitly when the
-- grammar feedback actually matters.
local LANGS = {
  { spell = "pt_br,en_us", ltex = "auto",  label = "pt-BR + en-US" },
  { spell = "pt_br",       ltex = "pt-BR", label = "pt-BR" },
  { spell = "en_us",       ltex = "en-US", label = "en-US" },
}

-- Choices survive restarts. Kept in stdpath("state") rather than the repo: it's
-- per-machine runtime state, not configuration to sync between the two boxes.
local STATE_FILE = vim.fn.stdpath("state") .. "/zenha-spell.json"

--- @class SpellState
--- @field lang_idx integer index into LANGS
--- @field enabled boolean|nil nil = never chosen (prose-only default)
local state = { lang_idx = 1, enabled = nil }

local function load_state()
  local f = io.open(STATE_FILE, "r")
  if not f then
    return
  end
  local raw = f:read("*a")
  f:close()
  local ok, data = pcall(vim.json.decode, raw)
  if ok and type(data) == "table" then
    if type(data.lang_idx) == "number" and LANGS[data.lang_idx] then
      state.lang_idx = data.lang_idx
    end
    if type(data.enabled) == "boolean" then
      state.enabled = data.enabled
    end
  end
end

local function save_state()
  local ok, encoded = pcall(vim.json.encode, state)
  if not ok then
    return
  end
  local f = io.open(STATE_FILE, "w")
  if not f then
    return
  end
  f:write(encoded)
  f:close()
end

load_state()

-- Restore. `vim.opt` behaves like :set -- it writes the window-local value AND
-- the default new windows inherit, so the restored choice applies everywhere,
-- not just whichever window happens to exist at startup.
vim.opt.spelllang = LANGS[state.lang_idx].spell
vim.opt.spell = state.enabled == true
-- Split camelCase/PascalCase into words, so identifiers in code don't drown the
-- checker in false positives when spell is on in a source file.
vim.opt.spelloptions = "camel"

--- Is the word under the cursor misspelled?
--- @return string|nil the bad word, or nil
local function bad_word_under_cursor()
  if not vim.wo.spell then
    return nil
  end
  local cword = vim.fn.expand("<cword>")
  if cword == "" then
    return nil
  end
  -- spellbadword({word}) checks that exact string, unlike the argument-less
  -- form, which would also match a bad word *after* the cursor and make F4
  -- act on something the cursor isn't on.
  local res = vim.fn.spellbadword(cword)
  return res[1] ~= "" and res[1] or nil
end

--- Suggestion picker for the word under the cursor.
--- Replacement goes through `{count}z=` rather than a manual substitution, so
--- Vim handles the edit (position, case, multibyte) exactly as it would natively.
--- @return boolean handled
function M.suggest()
  local word = bad_word_under_cursor()
  if not word then
    return false
  end

  local suggestions = vim.fn.spellsuggest(word, 15)
  if vim.tbl_isempty(suggestions) then
    vim.notify("Sem sugestões para '" .. word .. "'", vim.log.levels.INFO)
    return true
  end

  vim.ui.select(suggestions, {
    prompt = "Sugestões para '" .. word .. "':",
    format_item = function(item)
      return item
    end,
  }, function(choice, idx)
    if not choice then
      return
    end
    vim.cmd("normal! " .. idx .. "z=")
  end)
  return true
end

--- <F4>: fix whatever is under the cursor. A misspelling takes priority; when
--- there isn't one (or spell is off) this falls through to the LSP code action
--- that <F4> has always done.
function M.smart_action()
  if M.suggest() then
    return
  end
  vim.lsp.buf.code_action()
end

--- Toggle spell for the current window.
function M.toggle()
  local on = not vim.wo.spell
  vim.opt.spell = on
  vim.wo.spell = on
  state.enabled = on
  save_state()
  vim.notify(
    ("Corretor ortográfico: %s (%s)"):format(on and "ON" or "OFF", vim.bo.spelllang),
    vim.log.levels.INFO
  )
end

--- Push a language to any running LTeX+ client, so grammar follows the speller.
--- @param code string BCP-47 code, or "auto"
local function set_ltex_language(code)
  for _, client in ipairs(vim.lsp.get_clients({ name = "ltex_plus" })) do
    local settings = vim.tbl_deep_extend("force", client.settings or {}, {
      ltex = { language = code },
    })
    client.settings = settings
    pcall(function()
      client:notify("workspace/didChangeConfiguration", { settings = settings })
    end)
  end
end

--- Cycle both engines: pt+en -> pt -> en. Enables spell if it was off, since
--- changing the language while off gives no visible feedback.
function M.cycle_lang()
  local current = vim.bo.spelllang
  local idx = 1
  for i, l in ipairs(LANGS) do
    if l.spell == current then
      idx = i % #LANGS + 1
      break
    end
  end
  local mode = LANGS[idx]
  vim.opt.spelllang = mode.spell
  vim.opt.spell = true
  vim.wo.spell = true
  state.lang_idx = idx
  state.enabled = true
  save_state()
  set_ltex_language(mode.ltex)
  vim.notify(
    ("Idioma: %s  (gramática: %s)"):format(mode.label, mode.ltex),
    vim.log.levels.INFO
  )
end

--- Fetch the dictionary. Needed once per machine if spell/ isn't committed.
function M.fetch_dict()
  vim.fn.mkdir(SPELL_DIR, "p")
  local out = SPELL_DIR .. "/pt.utf-8.spl"
  vim.notify("Baixando dicionário pt-BR…", vim.log.levels.INFO)
  vim.system({ "curl", "-fsSL", "--max-time", "120", "-o", out, SPELL_URL }, {}, function(res)
    vim.schedule(function()
      if res.code == 0 then
        vim.notify("Dicionário pt-BR instalado em " .. out, vim.log.levels.INFO)
        vim.cmd("silent! setlocal spelllang=" .. vim.bo.spelllang)
      else
        vim.notify("Falha ao baixar o dicionário: " .. (res.stderr or ""), vim.log.levels.ERROR)
      end
    end)
  end)
end

vim.api.nvim_create_user_command("SpellFetchPtBr", M.fetch_dict, {
  desc = "Download the pt-BR spell dictionary",
})

local PROSE = {
  markdown = true,
  mdx = true,
  gitcommit = true,
  text = true,
  tex = true,
  rst = true,
  asciidoc = true,
}

-- Decides spell for EVERY filetype, not just prose. It has to set the "off"
-- case too: 'spell' is window-local, so opening a .lua file in the window that
-- was just showing markdown would otherwise inherit spell=true and light up
-- every identifier.
vim.api.nvim_create_autocmd("FileType", {
  group = vim.api.nvim_create_augroup("zenha.spell", { clear = true }),
  pattern = "*",
  callback = function(args)
    -- An explicit <leader>ts is a global choice and wins everywhere; only fall
    -- back to the prose-only default while the user has never toggled.
    if state.enabled ~= nil then
      vim.opt_local.spell = state.enabled
      return
    end
    vim.opt_local.spell = PROSE[vim.bo[args.buf].filetype] == true
  end,
  desc = "Spell on for prose filetypes, off elsewhere (unless explicitly toggled)",
})

vim.keymap.set("n", "<leader>ts", M.toggle, { desc = "Toggle corretor ortográfico" })
vim.keymap.set("n", "<leader>tl", M.cycle_lang, { desc = "Cycle spell language (pt_br/en_us/both)" })
-- <F4> itself is bound in plugins/code.lua, where the LSP keymaps live, so the
-- LSP config can't clobber it by load order.

return M
