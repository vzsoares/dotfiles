return {
  {
    'nvim-treesitter/nvim-treesitter',
    branch = 'main',
    build = ':TSUpdate',
    lazy = false,
    dependencies = {
      'windwp/nvim-ts-autotag',
    },
    config = function()
      local ts = require('nvim-treesitter')
      ts.setup()

      local ensure_installed = {
        'vimdoc', 'javascript', 'typescript', 'tsx',
        'c', 'lua', 'rust', 'go', 'html', 'json', 'yaml',
      }

      -- Track in-flight installs so a parser is never installed twice at once,
      -- and queue per-buffer callbacks to run once the install finishes.
      local installing = {}
      local function ensure(lang, on_done)
        if vim.tbl_contains(ts.get_installed(), lang) then
          if on_done then on_done() end
          return
        end
        if installing[lang] then
          if on_done then table.insert(installing[lang], on_done) end
          return
        end
        installing[lang] = on_done and { on_done } or {}
        ts.install(lang):await(vim.schedule_wrap(function()
          local cbs = installing[lang] or {}
          installing[lang] = nil
          for _, cb in ipairs(cbs) do cb() end
        end))
      end

      -- Pre-install the configured parsers.
      for _, lang in ipairs(ensure_installed) do
        ensure(lang)
      end

      -- Neovim itself provides treesitter highlighting (`:h treesitter-highlight`);
      -- turn it on per buffer, auto-installing an available-but-missing parser
      -- first (replaces the old `auto_install = true`).
      local function start(buf)
        if not vim.api.nvim_buf_is_valid(buf) then return end
        local lang = vim.treesitter.language.get_lang(vim.bo[buf].filetype)
        if not lang or not vim.tbl_contains(ts.get_available(), lang) then return end
        ensure(lang, function()
          if vim.api.nvim_buf_is_valid(buf) then
            pcall(vim.treesitter.start, buf, lang)
          end
        end)
      end

      vim.api.nvim_create_autocmd('FileType', {
        callback = function(args) start(args.buf) end,
      })

      -- Buffers already open before this config ran (e.g. the file nvim launched with).
      for _, buf in ipairs(vim.api.nvim_list_bufs()) do
        if vim.api.nvim_buf_is_loaded(buf) then start(buf) end
      end
    end
  },
  {
    'windwp/nvim-ts-autotag',
    event = { "BufReadPost", "BufNewFile" },
    opts = {
      -- Defaults
      enable_close = true,      -- Auto close tags
      enable_rename = true,     -- Auto rename pairs of tags
      enable_close_on_slash = false -- Auto close on trailing </
    },
    config = function(_, opts)
      require('nvim-ts-autotag').setup({
        opts = opts,
        -- Also override individual filetype configs, these take priority.
        -- Empty by default, useful if one of the "opts" global settings
        -- doesn't work well in a specific filetype
        per_filetype = {
          ["html"] = {
            enable_close = false
          }
        }
      })
    end
  },
  { 'nvim-treesitter/nvim-treesitter-context', lazy = false },
}
