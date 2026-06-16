-- Expand the TypeScript type under the cursor into its full structural form.
--
-- tsserver normally prints alias *names* (`User`) instead of their structure.
-- We force expansion by appending `type _ = ExpandRecursively<TARGET>` to the
-- buffer in-memory (off-screen, at EOF, so types in module scope resolve), then
-- hovering that alias with a manual LSP request. The injected lines are removed
-- as soon as the response comes back, and the float is shown right at your
-- cursor -- your view never moves.
--
-- UX -- a single smart K drives the whole thing:
--   K            -> normal hover (alias names)
--   K (again)    -> enter the float to scroll (builtin)
--   K (in float) -> expand the same symbol structurally

local M = {}

local FT = {
    typescript = true,
    typescriptreact = true,
    javascript = true,
    javascriptreact = true,
}

-- Recursive structural expander. Functions are passed through; arrays are
-- expanded element-wise (mapping over a raw array type would otherwise splatter
-- all its prototype methods into the result).
local UTIL = {
    "type __ExpandRec<T> = T extends (...a: any[]) => any",
    "    ? T",
    "    : T extends Array<infer U>",
    "        ? Array<__ExpandRec<U>>",
    "        : T extends object",
    "            ? T extends infer O ? { [K in keyof O]: __ExpandRec<O[K]> } : never",
    "            : T;",
}
local ALIAS = "__ExpandedType"
local NS = vim.api.nvim_create_namespace("expand_type")

local function expand(target, is_value, enter)
    local buf = vim.api.nvim_get_current_buf()
    if not FT[vim.bo[buf].filetype] then
        vim.notify("expand-type: not a TS/JS buffer", vim.log.levels.WARN)
        return
    end
    if not vim.bo[buf].modifiable then
        vim.notify("expand-type: buffer is not modifiable", vim.log.levels.WARN)
        return
    end
    if not next(vim.lsp.get_clients({ bufnr = buf, method = "textDocument/hover" })) then
        vim.notify("expand-type: no LSP client attached", vim.log.levels.WARN)
        return
    end

    local expr = is_value and ("typeof " .. target) or target
    local modified = vim.bo[buf].modified
    local first = vim.api.nvim_buf_line_count(buf) -- 0-based append point

    local lines = { "" }
    vim.list_extend(lines, UTIL)
    table.insert(lines, string.format("type %s = __ExpandRec<%s>;", ALIAS, expr))

    -- Append off-screen, kept out of undo history, tracked by an extmark so we
    -- can find the block again even if it shifts.
    local ul = vim.bo[buf].undolevels
    vim.bo[buf].undolevels = -1
    vim.api.nvim_buf_set_lines(buf, first, first, false, lines)
    vim.bo[buf].undolevels = ul
    local mark = vim.api.nvim_buf_set_extmark(buf, NS, first, 0, {})

    local alias_line = vim.api.nvim_buf_line_count(buf) - 1 -- 0-based, last line
    local alias_col = #"type " -- 0-based start of the alias identifier

    local function cleanup()
        if not vim.api.nvim_buf_is_valid(buf) then
            return
        end
        local pos = vim.api.nvim_buf_get_extmark_by_id(buf, NS, mark, {})
        local from = pos[1] or first
        local ul2 = vim.bo[buf].undolevels
        vim.bo[buf].undolevels = -1
        pcall(vim.api.nvim_buf_set_lines, buf, from, vim.api.nvim_buf_line_count(buf), false, {})
        vim.bo[buf].undolevels = ul2
        vim.api.nvim_buf_del_extmark(buf, NS, mark)
        vim.bo[buf].modified = modified
    end

    local params = {
        textDocument = vim.lsp.util.make_text_document_params(buf),
        position = { line = alias_line, character = alias_col },
    }

    -- Wait for the debounced didChange to reach the server, then hover the alias.
    vim.defer_fn(function()
        vim.lsp.buf_request_all(buf, "textDocument/hover", params, function(results)
            cleanup()
            local contents
            for _, res in pairs(results or {}) do
                local r = res.result
                if r and r.contents then
                    contents = vim.lsp.util.convert_input_to_markdown_lines(r.contents)
                    break
                end
            end
            if not contents or vim.tbl_isempty(contents) then
                vim.notify("expand-type: no type info for '" .. target .. "'", vim.log.levels.INFO)
                return
            end
            local _, fwin = vim.lsp.util.open_floating_preview(contents, "markdown", {
                border = "rounded",
                focusable = true,
                focus_id = "textDocument/hover",
                wrap = true,
                max_width = math.floor(vim.o.columns * 0.8),
            })
            -- Land inside the expanded float so it can be scrolled right away,
            -- without the extra K to re-enter it.
            if enter and fwin and vim.api.nvim_win_is_valid(fwin) then
                pcall(vim.api.nvim_set_current_win, fwin)
            end
        end)
    end, 220)
end

local function is_value_name(word)
    -- Convention heuristic: lowercase first letter → value (use `typeof`),
    -- uppercase → type. Use visual mode for anything more complex.
    return word:sub(1, 1):match("%l") ~= nil
end

-- Remembered symbol from the last plain hover, so a later K (inside the float)
-- knows what to expand.
local last = nil

-- The single K entry point: hover when in a normal window, expand when already
-- inside a floating window.
function M.smart_hover()
    local in_float = vim.api.nvim_win_get_config(0).relative ~= ""
    if in_float then
        if last and last.target then
            pcall(vim.api.nvim_win_close, 0, true) -- leave the float, back to source
            expand(last.target, last.is_value, true) -- enter the expanded float
        end
        return
    end

    local buf = vim.api.nvim_get_current_buf()
    if FT[vim.bo[buf].filetype] then
        local word = vim.fn.expand("<cword>")
        last = (word ~= "") and { target = word, is_value = is_value_name(word) } or nil
    else
        last = nil
    end
    vim.lsp.buf.hover()
end

vim.keymap.set("n", "K", M.smart_hover, { desc = "Hover / expand type (K K K)" })

return M
