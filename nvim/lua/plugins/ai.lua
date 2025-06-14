return {
    -- Copilot
    {
        'zbirenbaum/copilot.lua',
        lazy = false,
        -- event = "VeryLazy",
        build = ":Copilot auth",
        opts = {
            suggestion = {
                enabled = true,
                auto_trigger = true,
                hide_during_completion = true,
                debounce = 75,
                keymap = {
                    accept = "<Tab>",
                    accept_word = false,
                    accept_line = false,
                    next = "<M-]>",
                    prev = "<M-[>",
                    dismiss = "<C-]>",
                },
            },
        },
    },

    -- Render Markdown
    { 'MeanderingProgrammer/render-markdown.nvim', lazy = false },

    -- Avante
    {
        'yetone/avante.nvim',
        lazy = false,
        -- event = "VeryLazy",
        -- version = false,
        build = "make",
        dependencies = {
            'nvim-treesitter/nvim-treesitter',
            'nvim-lua/plenary.nvim',
            'MunifTanjim/nui.nvim',
        },
        opts = {
            provider = "copilot",
            providers = {
                copilot = {
                    model = "claude-3.7-sonnet",
                    extra_request_body = {
                        max_tokens = 90000,
                    },
                },
            },
            behaviour = {
                enable_cursor_planning_mode = true,
            },
            windows = {
                width = 40,
                ask = {
                    start_insert = false,
                },
            },
            mappings = {
                diff = {
                    ours = "to",
                    theirs = "tt",
                    all_theirs = "ta",
                    both = "tb",
                    cursor = "tt",
                    next = "]x",
                    prev = "[x",
                },
                suggestion = {
                    accept = "<M-l>",
                    next = "<M-]>",
                    prev = "<M-[>",
                    dismiss = "<C-]>",
                },
                jump = {
                    next = "]]",
                    prev = "[[",
                },
                submit = {
                    normal = "<CR>",
                    insert = "<C-s>",
                },
                sidebar = {
                    apply_all = "A",
                    apply_cursor = "a",
                    switch_windows = "<Tab>",
                    reverse_switch_windows = "<S-Tab>",
                },
            },
        },
    },
}
