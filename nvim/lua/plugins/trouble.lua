return {
  {
    "folke/trouble.nvim",
    version = 'v2.10.0',
    event = "VeryLazy",
    keys = {
      { "<leader>xq", "<cmd>TroubleToggle quickfix<cr>", { desc = "Toggle Quickfix List" } },
      { "<leader>xx", "<cmd>TroubleToggle document_diagnostics<cr>", { desc = "Toggle Document Diagnostics" } },
    },
    opts = {
      -- your configuration options here
    },
    config = function(_, opts)
      require("trouble").setup(opts)
    end
  }
}
