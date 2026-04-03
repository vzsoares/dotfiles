local augroup = vim.api.nvim_create_augroup
local masterGroup = augroup('master', {})

local autocmd = vim.api.nvim_create_autocmd
local yank_group = augroup('HighlightYank', {})

-- Activate otter.nvim for JSX/TSX (LSP completion in Alpine.js injected regions)
autocmd('FileType', {
    group = masterGroup,
    pattern = { 'typescriptreact', 'javascriptreact' },
    callback = function()
        -- args: languages, completion, diagnostics (off to avoid false positives)
        require('otter').activate({ 'javascript' }, true, false)
    end,
})

autocmd('TextYankPost', {
    group = yank_group,
    pattern = '*',
    callback = function()
        vim.highlight.on_yank({
            higroup = 'IncSearch',
            timeout = 40
        })
    end
})
