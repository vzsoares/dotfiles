vim.opt.guicursor = "a:block,i:block-blinkwait0-blinkon100-blinkoff100"

vim.opt.nu = true
vim.opt.relativenumber = true

vim.opt.tabstop = 4
vim.opt.softtabstop = 4
vim.opt.shiftwidth = 4
vim.opt.expandtab = true

vim.opt.smartindent = true

vim.opt.swapfile = false
vim.opt.backup = false
vim.opt.undodir = os.getenv("HOME") .. "/.vim/undodir"
vim.opt.undofile = true

vim.opt.hlsearch = false
vim.opt.incsearch = true

vim.opt.termguicolors = true

vim.opt.scrolloff = 8
vim.opt.signcolumn = "yes"
vim.opt.isfname:append("@-@")

vim.opt.updatetime = 50

vim.opt.colorcolumn = "80"
vim.opt.textwidth = 80
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