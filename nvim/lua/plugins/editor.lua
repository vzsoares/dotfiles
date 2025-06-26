return {
    {
        'lewis6991/gitsigns.nvim',
        event = "VeryLazy",
        opts = {
            signs = {
                add = { text = '+' },
                change = { text = '~' },
                delete = { text = '_' },
                topdelete = { text = '‾' },
                changedelete = { text = '~' },
            },
        },
    },
    {
        'petertriho/nvim-scrollbar',
        event = "VeryLazy",
        dependencies = {
            'lewis6991/gitsigns.nvim',
        },
        opts = {
            handle = {
                blend = 10
            },
            marks = {
                Cursor = {
                    text = "█",
                    color = "#c4a7e7"
                }
            }
        },
        config = function(_, opts)
            require("scrollbar").setup(opts)
            require("scrollbar.handlers.gitsigns").setup()
        end
    },
    {
        'tpope/vim-fugitive',
        event = "VeryLazy",
        config = function()
            vim.keymap.set("n", "<leader>gs", vim.cmd.Git)

            local G_Fugitive = vim.api.nvim_create_augroup("G_Fugitive", {})

            local autocmd = vim.api.nvim_create_autocmd
            autocmd("BufWinEnter", {
                group = G_Fugitive,
                pattern = "*",
                callback = function()
                    if vim.bo.ft ~= "fugitive" then
                        return
                    end

                    local bufnr = vim.api.nvim_get_current_buf()
                    local opts = { buffer = bufnr, remap = false }
                    vim.keymap.set("n", "<leader>p", function()
                        vim.cmd.Git('push')
                    end, opts)

                    -- rebase always
                    vim.keymap.set("n", "<leader>P", function()
                        vim.cmd.Git({ 'pull', '--rebase' })
                    end, opts)

                    -- NOTE: It allows me to easily set the branch i am pushing and any tracking
                    -- needed if i did not set the branch up correctly
                    vim.keymap.set("n", "<leader>t", ":Git push -u origin ", opts)
                end,
            })
        end
    },
    {
        'nvim-tree/nvim-tree.lua',
        dependencies = { 'nvim-tree/nvim-web-devicons' },
        lazy = false,
        keys = {
            { "<C-h>",      ":NvimTreeToggle<cr>", { silent = true, noremap = true, desc = "Toggle NvimTree" } },
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
    },
    {
        'nvim-telescope/telescope.nvim',
        version = '0.1.5',
        dependencies = { 'nvim-lua/plenary.nvim' },
        event = "VeryLazy",
        keys = {
            { '<leader>pf', function() require('telescope.builtin').find_files() end, { desc = 'Find Files' } },
            { '<C-p>',      function() require('telescope.builtin').git_files() end,  { desc = 'Find Git Files' } },
            { '<leader>ps', function()
                require('telescope.builtin').grep_string({ search = vim.fn.input("Grep > ") })
            end,
                { desc = 'Grep String' }
            },
            { '<leader>vh', function() require('telescope.builtin').help_tags() end, { desc = 'Help Tags' } },
        },
        opts = {
            defaults = {
                layout_strategy = 'vertical',
                layout_config = {
                    width = 90,
                    height = 90
                },
            },
        },
        config = function(_, opts)
            require('telescope').setup(opts)
        end
    }
}
