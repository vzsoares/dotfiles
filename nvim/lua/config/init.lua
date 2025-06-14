return {
    {
        "nvim-lua/plenary.nvim",
        lazy = true,
        config = function()
            local augroup = vim.api.nvim_create_augroup
            local masterGroup = augroup('master', {})

            local autocmd = vim.api.nvim_create_autocmd
            local yank_group = augroup('HighlightYank', {})

            function R(name)
                require("plenary.reload").reload_module(name)
            end

            autocmd('TextYankPost', {
                group = yank_group,
                pattern = '*',
                callback = function()
                    vim.highlight.on_yank({
                        higroup = 'IncSearch',
                        timeout = 40,
                    })
                end,
            })

        end
    }
} 