return {
  {
    'nvim-tree/nvim-tree.lua',
    dependencies = { 'nvim-tree/nvim-web-devicons' },
    lazy = false,
    keys = {
      { "<C-h>", ":NvimTreeToggle<cr>", { silent = true, noremap = true, desc = "Toggle NvimTree" } },
      { "<leader>pv", ":NvimTreeToggle<cr>", { silent = true, noremap = true, desc = "Toggle NvimTree" } },
    },
    opts = {
      sort = {
        sorter = "case_sensitive",
      },
      view = {
        width = 100,
        relativenumber = true,
        number = true
      },
      renderer = {
        group_empty = true,
      },
      filters = {
        git_ignored = false,
        dotfiles = false,
      },
      actions = {
        open_file = {
          quit_on_open = true
        }
      },
      update_focused_file = {
        enable = true
      }
    },
    config = function(_, opts)
      local api = require("nvim-tree.api")

      local function my_on_attach(bufnr)
        local function opts(desc)
          return { desc = "nvim-tree: " .. desc, buffer = bufnr, noremap = true, silent = true, nowait = true }
        end

        -- default mappings
        api.config.mappings.default_on_attach(bufnr)
      end

      opts.on_attach = my_on_attach
      require("nvim-tree").setup(opts)
    end
  }
}
