return {
  {
    'petertriho/nvim-scrollbar',
    event = "VeryLazy",
    dependencies = {
      'lewis6991/gitsigns.nvim',
    },
    opts = {
      handle = {
        blend = 10
      },
      marks = {
        Cursor = {
          text = "█",
          color = "#c4a7e7"
        }
      }
    },
    config = function(_, opts)
      require("scrollbar").setup(opts)
      require("scrollbar.handlers.gitsigns").setup()
    end
  }
}
