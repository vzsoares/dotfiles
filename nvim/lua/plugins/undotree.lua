return {
  {
    'mbbill/undotree',
    event = "VeryLazy",
    keys = {
      { "<leader>u", vim.cmd.UndotreeToggle, { desc = "Toggle Undotree" } },
    },
    config = function()
      vim.g.undotree_SetFocusWhenToggle = 1
    end
  }
}

