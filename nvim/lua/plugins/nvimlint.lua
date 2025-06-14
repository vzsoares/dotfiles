return {
  {
    'mfussenegger/nvim-lint',
    event = { "BufWritePost", "BufReadPost", "InsertLeave" },
    opts = {
      linters_by_ft = {
        markdown = { 'markdownlint' },
        python = { 'ruff', 'flake8' }
      },
      linters = {
        markdownlint = {
          args = {
            '--disable=MD030',
            '-'
          }
        }
      }
    },
    config = function(_, opts)
      require('lint').linters_by_ft = opts.linters_by_ft
      require('lint').linters.markdownlint.args = opts.linters.markdownlint.args

      vim.api.nvim_create_autocmd({ "BufWritePost" }, {
        callback = function()
          require("lint").try_lint()
        end,
      })
    end
  }
}
