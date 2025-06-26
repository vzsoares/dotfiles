return {
    {
        "ThePrimeagen/harpoon",
        branch = "harpoon2",
        dependencies = { "nvim-lua/plenary.nvim" },
        event = "VeryLazy",
        opts = {
            settings = {
                save_on_toggle = true,
                sync_on_ui_close = true,
            }
        },
        config = function(_, opts)
            local harpoon = require("harpoon")
            harpoon:setup(opts)

            -- keymap
            vim.keymap.set("n", "<C-e>", function() harpoon.ui:toggle_quick_menu(harpoon:list()) end)

            vim.keymap.set("n", "<leader>a", function() harpoon:list():add() end)

            -- vim.keymap.set("n", "<C-h>", function() harpoon:list():select(1) end)
            vim.keymap.set("n", "<C-t>", function() harpoon:list():select(2) end)
            vim.keymap.set("n", "<C-n>", function() harpoon:list():select(3) end)
            vim.keymap.set("n", "<C-s>", function() harpoon:list():select(4) end)

            -- Toggle previous & next buffers stored within Harpoon list
            vim.keymap.set("n", "<C-S-P>", function() harpoon:list():prev() end)
            vim.keymap.set("n", "<C-S-N>", function() harpoon:list():next() end)
        end
    },
    { 'nvim-lua/plenary.nvim',     lazy = false },
    { 'MunifTanjim/nui.nvim',      lazy = false },
    { 'HakonHarnes/img-clip.nvim', lazy = false },
    {
        'laytan/cloak.nvim',
        event = "VeryLazy",
        opts = {
            enabled = false,
            cloak_character = "*",
            -- The applied highlight group (colors) on the cloaking, see `:h highlight`.
            highlight_group = "Comment",
            patterns = {
                {
                    -- Match any file starting with ".env".
                    -- This can be a table to match multiple file patterns.
                    file_pattern = {
                        ".env*",
                        "wrangler.toml",
                        ".dev.vars",
                    },
                    -- Match an equals sign and any character after it.
                    -- This can also be a table of patterns to cloak,
                    -- example: cloak_pattern = { ":.+", "-.+" } for yaml files.
                    cloak_pattern = "=.+"
                },
            },
        },
    },
    {
        'mbbill/undotree',
        event = "VeryLazy",
        keys = {
            { "<leader>u", vim.cmd.UndotreeToggle, { desc = "Toggle Undotree" } },
        },
        config = function()
            vim.g.undotree_SetFocusWhenToggle = 1
        end
    },
    {
        "folke/trouble.nvim",
        version = 'v2.10.0',
        event = "VeryLazy",
        keys = {
            { "<leader>xq", "<cmd>TroubleToggle quickfix<cr>",             { desc = "Toggle Quickfix List" } },
            { "<leader>xx", "<cmd>TroubleToggle document_diagnostics<cr>", { desc = "Toggle Document Diagnostics" } },
        },
        opts = {
            -- your configuration options here
        },
        config = function(_, opts)
            require("trouble").setup(opts)
        end
    }
}
