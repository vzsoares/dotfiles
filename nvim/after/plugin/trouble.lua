vim.keymap.set("n","<leader>xq", "<cmd>TroubleToggle quickfix<cr>",
  {silent = true, noremap = true}
)

vim.keymap.set("n","<leader>xx", "<cmd>TroubleToggle document_diagnostics<cr>",
  {silent = true, noremap = true}
)
