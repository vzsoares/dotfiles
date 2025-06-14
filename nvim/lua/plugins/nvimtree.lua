return {
  {
    'nvim-tree/nvim-tree.lua',
    dependencies = { 'nvim-tree/nvim-web-devicons' },
    event = "VeryLazy",
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

      vim.keymap.set("n", "<C-h>", ":NvimTreeToggle<cr>", { silent = true, noremap = true })
      vim.keymap.set("n", "<leader>pv", ":NvimTreeToggle<cr>", { silent = true, noremap = true })
    end
  }
}
