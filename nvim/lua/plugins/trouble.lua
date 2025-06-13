return {
    "folke/trouble.nvim",
    version = 'v2.10.0',
    lazy = true,
    config = function()
        vim.keymap.set("n","<leader>xq", "<cmd>TroubleToggle quickfix<cr>",
          {silent = true, noremap = true}
        )

        vim.keymap.set("n","<leader>xx", "<cmd>TroubleToggle document_diagnostics<cr>",
          {silent = true, noremap = true}
        )
    end
}
