-- Typst helpers around the tinymist LSP (configured in plugins.code).
--
-- tinymist compiles whatever file is focused. That is right for a one-file
-- document and wrong for a book, where chapters are `#include`d and only make
-- sense compiled through the entry file -- saving a chapter would otherwise
-- export a PDF of that chapter alone. `tinymist.pinMain` pins the entry so the
-- whole book rebuilds on every save, wherever the cursor happens to be.

local M = {}

--- @return vim.lsp.Client|nil
local function tinymist()
  local client = vim.lsp.get_clients({ name = "tinymist", bufnr = 0 })[1]
  if not client then
    vim.notify("tinymist is not attached to this buffer", vim.log.levels.WARN)
  end
  return client
end

--- Pin `path` (default: the current buffer) as the project's main file.
--- @param path string|nil
function M.pin(path)
  local client = tinymist()
  if not client then
    return
  end
  path = (path and path ~= "") and vim.fn.fnamemodify(path, ":p") or vim.api.nvim_buf_get_name(0)
  client:exec_cmd({
    title = "pin main file",
    command = "tinymist.pinMain",
    arguments = { path },
  }, { bufnr = 0 })
  vim.notify("Typst main file: " .. vim.fn.fnamemodify(path, ":~:."))
end

--- Drop the pin; tinymist goes back to compiling the focused file.
function M.unpin()
  local client = tinymist()
  if not client then
    return
  end
  -- vim.v.null (not nil) -- the argument has to survive JSON encoding as null.
  client:exec_cmd({
    title = "unpin main file",
    command = "tinymist.pinMain",
    arguments = { vim.v.null },
  }, { bufnr = 0 })
  vim.notify("Typst main file unpinned")
end

--- Open the PDF next to the current .typ file in the OS viewer. Both machines
--- are covered: `open` on macOS, `xdg-open` under i3.
function M.open_pdf()
  local pdf = vim.fn.expand("%:p:r") .. ".pdf"
  if vim.fn.filereadable(pdf) == 0 then
    vim.notify("No PDF yet at " .. vim.fn.fnamemodify(pdf, ":~:.") .. " -- save once to export", vim.log.levels.WARN)
    return
  end
  local opener = vim.fn.has("mac") == 1 and "open" or "xdg-open"
  vim.system({ opener, pdf }, { detach = true })
end

vim.api.nvim_create_user_command("TypstPin", function(args)
  M.pin(args.args)
end, { nargs = "?", complete = "file", desc = "Pin the Typst entry file (book root) for tinymist" })

vim.api.nvim_create_user_command("TypstUnpin", M.unpin, {
  desc = "Unpin the Typst entry file",
})

vim.api.nvim_create_user_command("TypstOpen", M.open_pdf, {
  desc = "Open the PDF exported from this Typst file",
})

return M
