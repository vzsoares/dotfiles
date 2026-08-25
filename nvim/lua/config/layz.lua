-- Neovim version guard -------------------------------------------------------
-- nvim-treesitter is pinned to its fast-moving `main` branch, whose parser
-- installer uses APIs that only exist in Neovim 0.12+ (e.g. `vim.list`). An
-- older nvim silently breaks treesitter, so enforce a supported version window
-- up front instead. Bump these when you validate a newer nvim.
local nvim_min = { 0, 12, 0 } -- hard floor: below this, refuse to load plugins
local nvim_max = { 0, 13, 0 } -- soft ceiling: at/above this is untested, warn only
local nvim_cur = vim.version()

if vim.version.lt(nvim_cur, nvim_min) then
    vim.api.nvim_echo({
        { ("Neovim >= %d.%d required, but this is %s.\n"):format(nvim_min[1], nvim_min[2], tostring(nvim_cur)), "ErrorMsg" },
        { "Upgrade nvim (e.g. `brew upgrade neovim`) before loading the config.", "WarningMsg" },
    }, true, {})
    return
elseif vim.version.ge(nvim_cur, nvim_max) then
    vim.schedule(function()
        vim.notify(
            ("Neovim %s is newer than the tested ceiling %d.%d — plugins may need a version bump.")
                :format(tostring(nvim_cur), nvim_max[1], nvim_max[2]),
            vim.log.levels.WARN
        )
    end)
end

require("config.options")

-- Bootstrap lazy.nvim
local lazypath = vim.fn.stdpath("data") .. "/lazy/lazy.nvim"
if not (vim.uv or vim.loop).fs_stat(lazypath) then
    local lazyrepo = "https://github.com/folke/lazy.nvim.git"
    local out = vim.fn.system({ "git", "clone", "--filter=blob:none", "--branch=stable", lazyrepo, lazypath })
    if vim.v.shell_error ~= 0 then
        vim.api.nvim_echo({ { "Failed to clone lazy.nvim:\n", "ErrorMsg" }, { out, "WarningMsg" },
            { "\nPress any key to exit..." } }, true, {})
        vim.fn.getchar()
        os.exit(1)
    end
end
vim.opt.rtp:prepend(lazypath)

-- Make sure to setup `mapleader` and `maplocalleader` before
-- loading lazy.nvim so that mappings are correct.
-- This is also a good place to setup other settings (vim.opt)
vim.g.mapleader = " "
vim.g.maplocalleader = "\\"

require("config.keymaps")
require("config.misc")
require("config.help")
require("config.expand_type")
require("config.spell")

-- Setup lazy.nvim
require("lazy").setup({
    spec = { -- import your plugins
        {
            import = "plugins"
        } },
    defaults = {
        lazy = false,
        version = false
    },
    install = {
        colorscheme = { "habamax" }
    },
    checker = {
        enabled = false
    },
    performance = {
        rtp = {
            disabled_plugins = { "gzip", "tarPlugin", "tohtml", "tutor", "zipPlugin" }
        }
    },
    ui = {
        border = "rounded"
    },
    change_detection = {
        notify = false
    }
})
