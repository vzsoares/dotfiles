local api = require("nvim-tree.api")

local function my_on_attach(bufnr)
    local function opts(desc)
        return { desc = "nvim-tree: " .. desc, buffer = bufnr, noremap = true, silent = true, nowait = true }
    end

    -- default mappings
    api.config.mappings.default_on_attach(bufnr)

    -- custom mappings
end

require("nvim-tree").setup({
    on_attach = my_on_attach,
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
})

vim.api.nvim_set_keymap("n", "<C-h>", ":NvimTreeToggle<cr>", { silent = true, noremap = true })
vim.api.nvim_set_keymap("n", "<leader>pv", ":NvimTreeToggle<cr>", { silent = true, noremap = true })
