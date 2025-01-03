require("master.set")
require("master.remap")
require("master.packer")

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

-- disable netrw at the very start of your init.lua
vim.g.loaded_netrw = 1
vim.g.loaded_netrwPlugin = 1

vim.filetype.add({ pattern = { [".*%.ansible%..*"] = "yaml.ansible" } })
vim.filetype.add({
    extension = {
        gotmpl = 'gotmpl',
    },
    pattern = {
        [".*%.go.tmpl"] = "gotmpl"
    },
})
