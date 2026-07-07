-- Expand the TypeScript type under the cursor into its full structural form.
--
-- tsserver normally prints alias *names* (`User`) instead of their structure,
-- and its *hover* output is truncated at a hard-coded budget (`... N more ...`,
-- collapsed `{ ...; }`) that no compiler option can lift. Only the *diagnostic*
-- (error-message) path honours `noErrorTruncation`, so we force a type error:
-- we append, off-screen at EOF, a probe that assigns the recursively-expanded
-- type to `1`. tsserver then reports "Type '<full structure>' is not assignable
-- to type '1'." -- with `noErrorTruncation: true` in the project's tsconfig the
-- `<full structure>` is complete. We read it from the diagnostic, strip the
-- injected lines, pretty-print it, and float it at your cursor.
--
-- REQUIRES `"noErrorTruncation": true` in the project's tsconfig compilerOptions
-- (editor-only; safe under `noEmit`). Without it the expansion is still
-- truncated -- that limit lives in tsserver, not here.
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
local PROBE = "const __ExpandProbe: 1 = (0 as unknown as __ExpandRec<%s>);"
local NS = vim.api.nvim_create_namespace("expand_type")

-- Pretty-print tsserver's single-line type string into indented form.
local function pretty(s)
    local res, depth, buf, bol = {}, 0, {}, false
    local function flush()
        res[#res + 1] = table.concat(buf)
        buf = {}
    end
    local function nl()
        flush()
        res[#res + 1] = "\n" .. string.rep("    ", depth)
        bol = true
    end
    local i, n = 1, #s
    while i <= n do
        local c, nxt = s:sub(i, i), s:sub(i + 1, i + 1)
        if bol and c == " " then
            -- swallow leading spaces after a line break
        elseif c == "{" and nxt == "}" then
            buf[#buf + 1] = "{}"
            i = i + 1
            bol = false
        elseif c == "{" then
            buf[#buf + 1] = "{"
            depth = depth + 1
            nl()
        elseif c == "}" then
            depth = math.max(depth - 1, 0)
            nl()
            res[#res + 1] = "}"
            bol = false
        elseif c == ";" then
            buf[#buf + 1] = ";"
            nl()
        else
            buf[#buf + 1] = c
            bol = false
        end
        i = i + 1
    end
    flush()
    return (table.concat(res):gsub("\n%s*\n", "\n"))
end

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
    table.insert(lines, string.format(PROBE, expr))

    -- Append off-screen, kept out of undo history, tracked by an extmark so we
    -- can find the block again even if it shifts.
    local ul = vim.bo[buf].undolevels
    vim.bo[buf].undolevels = -1
    vim.api.nvim_buf_set_lines(buf, first, first, false, lines)
    vim.bo[buf].undolevels = ul
    local mark = vim.api.nvim_buf_set_extmark(buf, NS, first, 0, {})

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

    -- Pull the expanded structure out of the probe's "not assignable" error.
    local function extract()
        for _, d in ipairs(vim.diagnostic.get(buf)) do
            if d.lnum >= first then
                local t = d.message:match("^Type '(.*)' is not assignable to type '1'")
                if t then
                    return t
                end
            end
        end
        return nil
    end

    local function show(t)
        local body = pretty(t)
        local out = vim.split("type __ExpandedType = " .. body, "\n", { plain = true })
        local _, fwin = vim.lsp.util.open_floating_preview(out, "typescript", {
            border = "rounded",
            focusable = true,
            focus_id = "textDocument/hover",
            max_width = math.floor(vim.o.columns * 0.8),
            max_height = math.floor(vim.o.lines * 0.8),
        })
        if enter and fwin and vim.api.nvim_win_is_valid(fwin) then
            pcall(vim.api.nvim_set_current_win, fwin)
        end
    end

    -- The injected edit reaches tsserver after the debounced didChange, then it
    -- recomputes and publishes diagnostics. Poll until our probe error lands.
    local tries = 0
    local function poll()
        if not vim.api.nvim_buf_is_valid(buf) then
            return
        end
        local t = extract()
        if t then
            cleanup()
            show(t)
            return
        end
        tries = tries + 1
        if tries < 30 then
            vim.defer_fn(poll, 80)
        else
            cleanup()
            vim.notify(
                "expand-type: no expansion for '" .. target .. "' "
                    .. "(any/unknown/primitive, or tsconfig missing noErrorTruncation)",
                vim.log.levels.INFO
            )
        end
    end
    vim.defer_fn(poll, 220)
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
