require('lint').linters_by_ft = {
    markdown = { 'markdownlint', },
    python = { 'flake8', }
}

local markdownlint = require('lint').linters.markdownlint
markdownlint.args = {
    '--disable=MD030',
    '-'
}

vim.api.nvim_create_autocmd({ "BufWritePost" }, {
    callback = function()
        -- try_lint without arguments runs the linters defined in `linters_by_ft`
        -- for the current filetype
        require("lint").try_lint()
    end,
})
