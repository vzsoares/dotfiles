return {
  {
    'nvim-telescope/telescope.nvim',
    version = '0.1.5',
    dependencies = { 'nvim-lua/plenary.nvim' },
    event = "VeryLazy",
    keys = {
      { '<leader>pf', function() require('telescope.builtin').find_files() end, { desc = 'Find Files' } },
      { '<C-p>', function() require('telescope.builtin').git_files() end, { desc = 'Find Git Files' } },
      { '<leader>ps', function() 
          require('telescope.builtin').grep_string({ search = vim.fn.input("Grep > ") })
        end, 
        { desc = 'Grep String' }
      },
      { '<leader>vh', function() require('telescope.builtin').help_tags() end, { desc = 'Help Tags' } },
    },
    opts = {
      defaults = {
        layout_strategy = 'vertical',
        layout_config = {
          width = 90,
          height = 90
        },
      },
    },
    config = function(_, opts)
      require('telescope').setup(opts)
    end
  }
}
