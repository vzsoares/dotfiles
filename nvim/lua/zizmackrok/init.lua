require("zizmackrok.set")
require("zizmackrok.remap")
require("zizmackrok.packer")

local augroup = vim.api.nvim_create_augroup
local zizmackrokGroup = augroup('zizmackrok', {})

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

autocmd({"BufWritePre"}, {
    group = zizmackrokGroup,
    pattern = "*",
    command = [[%s/\s\+$//e]],
})

-- disable netrw at the very start of your init.lua
vim.g.loaded_netrw = 1
vim.g.loaded_netrwPlugin = 1

vim.filetype.add({ pattern = { [".*%.ansible%..*"] = "yaml.ansible" } })
