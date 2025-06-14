return {
  {
    'nvim-treesitter/nvim-treesitter',
    build = ':TSUpdate',
    lazy = false,
    event = { "BufReadPost", "BufNewFile" },
    dependencies = {
      'windwp/nvim-ts-autotag',
    },
    opts = {
      -- A list of parser names, or "all"
      ensure_installed = { "vimdoc", "javascript", "typescript", "c", "lua", "rust", "go" },

      -- Install parsers synchronously (only applied to `ensure_installed`)
      sync_install = false,

      -- Automatically install missing parsers when entering buffer
      -- Recommendation: set to false if you don't have `tree-sitter` CLI installed locally
      auto_install = true,

      highlight = {
        -- `false` will disable the whole extension
        enable = true,

        -- Setting this to true will run `:h syntax` and tree-sitter at the same time.
        -- Set this to `true` if you depend on 'syntax' being enabled (like for indentation).
        -- Using this option may slow down your editor, and you may see some duplicate highlights.
        -- Instead of true it can also be a list of languages
        additional_vim_regex_highlighting = false,
      },
    },
    config = function(_, opts)
      require('nvim-treesitter.configs').setup(opts)
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
  { 'nvim-treesitter/playground', lazy = false },
  { 'nvim-lua/plenary.nvim', lazy = false },
  {
    "folke/snacks.nvim",
    priority = 1000,
    lazy = false,
    ---@type snacks.Config
    opts = {
      -- your configuration comes here
      -- or leave it empty to use the default settings
      -- refer to the configuration section below
      bigfile = { enabled = true },
      dashboard = { enabled = true },
      explorer = { enabled = false },
      indent = { enabled = true },
      input = { enabled = true },
      picker = { enabled = true },
      notifier = { enabled = true },
      quickfile = { enabled = true },
      scope = { enabled = true },
      scroll = { enabled = false },
      statuscolumn = { enabled = true },
      words = { enabled = true },
    },
  },
  { 'MunifTanjim/nui.nvim', lazy = false },
  { 'HakonHarnes/img-clip.nvim', lazy = false },
}
