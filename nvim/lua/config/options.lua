vim.opt.guicursor = "a:block,i:block-blinkwait0-blinkon100-blinkoff100"

vim.opt.nu = true
vim.opt.relativenumber = true

vim.opt.tabstop = 4
vim.opt.softtabstop = 4
vim.opt.shiftwidth = 4
vim.opt.expandtab = true

vim.opt.smartindent = true
vim.opt.smartcase = true

vim.opt.swapfile = false
vim.opt.backup = false
vim.opt.undodir = os.getenv("HOME") .. "/.vim/undodir"
vim.opt.undofile = true
vim.opt.splitright = true

vim.opt.hlsearch = false
vim.opt.incsearch = true

vim.opt.termguicolors = true

vim.opt.scrolloff = 8
vim.opt.signcolumn = "yes"
vim.opt.isfname:append("@-@")

vim.opt.updatetime = 50

vim.opt.colorcolumn = "100"
vim.opt.textwidth = 100
vim.opt.wrapmargin = 2
vim.opt.wrap = true
vim.opt.linebreak = true

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


-- Diff: make a one-character change actually readable.
-- nvim already marks the changed region inside a line (diffopt inline:char), but
-- rose-pine paints DiffText almost the same shade as DiffChange and gives
-- DiffTextAdd an identical colour, so the marked region doesn't stand out.
-- Applies to fugitive's :Gdiffsplit (ds/dh) since that's a real diff split.
vim.opt.diffopt = {
    "internal",
    "filler",
    "closeoff",
    "indent-heuristic",
    "algorithm:histogram", -- fewer spurious whole-block hunks than the myers default
    "inline:char",         -- highlight the changed characters, not the whole line
    "linematch:60",        -- pair changed lines up better, so inline diffing has a real counterpart
}

-- DiffChange (the whole changed line) stays muted; DiffText / DiffTextAdd are
-- the actual edit and need to pop against it. Reapplied on every colorscheme
-- load, since :colorscheme resets highlight groups (see ColorMyPencils).
local function diff_highlights()
    vim.api.nvim_set_hl(0, "DiffText", { bg = "#7d3f52", fg = "#e0def4", bold = true })
    vim.api.nvim_set_hl(0, "DiffTextAdd", { bg = "#2f5d63", fg = "#e0def4", bold = true })
end

vim.api.nvim_create_autocmd("ColorScheme", { callback = diff_highlights })
diff_highlights()
