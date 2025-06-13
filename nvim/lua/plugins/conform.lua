return {
    "stevearc/conform.nvim",
    lazy = true,
    config = function()
        local builtin = require("conform")

        builtin.setup({
            formatters_by_ft = {
                lua = { "stylua" },
                python = { "black", "isort", "ruff" },
                javascript = { "prettier" },
                javascriptreact = { "prettier" },
                typescript = { "prettier" },
                typescriptreact = { "prettier" },
                markdown = { "prettier" },
                mdx = { "prettier" },
                json = { "prettier" },
                css = { "prettier" },
                html = { "prettier" },
                yaml = { "prettier" },
                sh = { "shfmt" },
                go = { "goimports" },
            },
        })

        builtin.formatters.prettier = {
            prepend_args = { "--tab-width", "4" },
        }

        vim.keymap.set("n", "<leader>f", function()
            builtin.format { async = true, lsp_fallback = true, timeout_ms = 2500 }
        end)
    end
}
