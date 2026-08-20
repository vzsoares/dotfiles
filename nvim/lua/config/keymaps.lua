vim.g.mapleader = " "

vim.keymap.set("v", "J", ":m '>+1<CR>gv=gv")
vim.keymap.set("v", "K", ":m '<-2<CR>gv=gv")

vim.keymap.set("n", "J", "mzJ`z")
vim.keymap.set("n", "<C-d>", "<C-d>zz")
vim.keymap.set("n", "<C-u>", "<C-u>zz")
vim.keymap.set("n", "n", "nzzzv")
vim.keymap.set("n", "N", "Nzzzv")

-- greatest remap ever
vim.keymap.set("x", "<leader>p", [["_dP]])

-- next greatest remap ever : asbjornHaland
vim.keymap.set({ "n", "v" }, "<leader>y", [["+y]])
vim.keymap.set("n", "<leader>Y", [["+Y]])

vim.keymap.set({ "n", "v" }, "<leader>d", [["_d]])

-- This is going to get me cancelled
vim.keymap.set("i", "<C-c>", "<Esc>")

-- Line-start / line-end without the dead-key dance: `^` is a dead key on the
-- ABNT layout. Mapped for operators too ("o"), so d/c/y take them as targets.
-- "x" not "v" on purpose: remapping select mode would break snippet placeholders.
vim.keymap.set({ "n", "x", "o" }, "H", "^")
vim.keymap.set({ "n", "x", "o" }, "L", "$")

vim.keymap.set("n", "Q", "<nop>")
vim.keymap.set("n", "<C-f>", "<cmd>silent !tmux neww tmux-sessionizer<CR>")
vim.keymap.set("n", "<leader>F", vim.lsp.buf.format)

vim.keymap.set("n", "<C-k>", "<cmd>cnext<CR>zz")
vim.keymap.set("n", "<C-j>", "<cmd>cprev<CR>zz")
vim.keymap.set("n", "<leader>k", "<cmd>lnext<CR>zz")
vim.keymap.set("n", "<leader>j", "<cmd>lprev<CR>zz")

vim.keymap.set("n", "<leader>s", [[:%s/\<<C-r><C-w>\>/<C-r><C-w>/gI<Left><Left><Left>]])

vim.keymap.set("n", "<C-z>", "", {
    silent = true
})

-- Reload the current config file. Bare `:so` executes the BUFFER as Vimscript,
-- so hitting this in any other filetype tries to run the source as Ex commands
-- (a .tsx buffer dies on `'use client';` with E481). Only source what can be
-- sourced.
vim.keymap.set("n", "<leader><leader>", function()
    local ft = vim.bo.filetype
    if ft ~= "lua" and ft ~= "vim" then
        vim.notify("Nothing to source: " .. (ft == "" and "no filetype" or ft), vim.log.levels.WARN)
        return
    end
    -- name the file explicitly rather than relying on the bare form
    local file = vim.api.nvim_buf_get_name(0)
    if file == "" then
        vim.notify("Nothing to source: buffer has no file", vim.log.levels.WARN)
        return
    end
    local ok, err = pcall(vim.cmd.source, file)
    vim.notify(ok and ("Sourced " .. vim.fn.fnamemodify(file, ":t")) or tostring(err),
        ok and vim.log.levels.INFO or vim.log.levels.ERROR)
end, { desc = "Source current file" })

vim.keymap.set("v", "<leader>ae", ":CodeCompanion /buffer ", { noremap = true, silent = false })
