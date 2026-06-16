-- Personal cheatsheet — floating help menu (open with <leader>H).
-- Lists my custom keymaps plus a few built-in tips worth remembering.
-- Keep this in sync when adding/changing keybinds in lua/plugins/* and keymaps.lua.

local M = {}

-- Each section: { title, { { lhs, desc }, ... } }
-- `lhs == ""` renders as a plain note line (for built-in tips).
local SECTIONS = {
    {
        "Git — Fugitive",
        {
            { "<leader>gs", "Open Git status (:Git)" },
            { "dv",         "Vertical diff of file under cursor (in status / :Gvdiffsplit)" },
            { "ds / dh",    "Horizontal diff split (:Gdiffsplit)" },
            { "dq",         "Close the diff" },
            { "=",          "Toggle inline diff under cursor (in status)" },
            { "s / u",      "Stage / unstage file under cursor (in status)" },
            { "cc",         "Commit  (ca = amend)" },
            { "<leader>p",  "Push       (in fugitive buffer)" },
            { "<leader>P",  "Pull --rebase (in fugitive buffer)" },
            { "<leader>t",  ":Git push -u origin … (set upstream, in fugitive)" },
            { "",           "Merge conflict 3-way: :Gvdiffsplit!  then dp / do" },
        },
    },
    {
        "Files & Navigation",
        {
            { "<C-h>",      "Toggle NvimTree" },
            { "<leader>pv", "Toggle NvimTree" },
            { "<C-p>",      "Find git files (Telescope)" },
            { "<leader>pf", "Find files (Telescope)" },
            { "<leader>ps", "Grep string in project (Telescope)" },
            { "<leader>vh", "Help tags (Telescope)" },
            { "<C-f>",      "tmux-sessionizer (new window)" },
        },
    },
    {
        "Harpoon (tabbed lists: alpha/bravo/charlie)",
        {
            { "<leader>a", "Add current file to active list" },
            { "<C-e>",     "Open harpoon picker (<Tab>/<S-Tab> switch tabs)" },
            { "<C-t>",     "Jump to mark 2" },
            { "<C-n>",     "Jump to mark 3" },
            { "<C-s>",     "Jump to mark 4" },
            { "<C-S-P>",   "Previous harpoon mark" },
            { "<C-S-N>",   "Next harpoon mark" },
            { "",          "In picker: dd / <C-d> delete · <C-j>/<C-k> reorder" },
        },
    },
    {
        "Search & Replace",
        {
            { "<leader>sr", "Project search/replace — grug-far (visual: prefill selection)" },
            { "<leader>sw", "Search/replace word under cursor — grug-far" },
            { "<leader>s",  "Substitute word under cursor (current file)" },
        },
    },
    {
        "LSP & Code",
        {
            { "gd",         "Go to definition" },
            { "gr",         "Go to references" },
            { "K",          "Hover docs" },
            { "<leader>vd", "Show diagnostic float" },
            { "<F2>",       "Rename symbol" },
            { "<F4>",       "Code action" },
            { "<leader>f",  "Format buffer (conform)" },
            { "<leader>F",  "Format buffer (LSP)" },
            { "<C-k> / <C-j>", "Quickfix next / prev" },
            { "<leader>k / <leader>j", "Loclist next / prev" },
            { "<leader>xq", "Trouble: quickfix" },
            { "<leader>xx", "Trouble: document diagnostics" },
        },
    },
    {
        "Editing",
        {
            { "J / K (visual)", "Move selection down / up" },
            { "J",           "Join line, keep cursor" },
            { "<leader>p (x)", "Paste over selection, keep register" },
            { "<leader>y / Y", "Yank to system clipboard" },
            { "<leader>d",   "Delete to black-hole register" },
            { "<C-d> / <C-u>", "Half-page down / up, centered" },
            { "n / N",       "Next / prev search, centered" },
        },
    },
    {
        "AI & Misc",
        {
            { "<leader>aa", "99: edit current file" },
            { "<leader>as", "99: search project" },
            { "<leader>ae", "99: process selection (visual)" },
            { "<leader>ax", "99: stop all requests" },
            { "<leader>9m", "99: select model" },
            { "<M-Tab>",    "Accept Copilot suggestion" },
            { "<leader>u",  "Toggle Undotree" },
            { "<leader><leader>", "Source current file (:so)" },
        },
    },
}

local function build_lines()
    local lines = { "  Cheatsheet   (q / <Esc> to close)", "" }
    local pad = 18
    for _, section in ipairs(SECTIONS) do
        lines[#lines + 1] = "  " .. section[1]
        for _, entry in ipairs(section[2]) do
            local lhs, desc = entry[1], entry[2]
            if lhs == "" then
                lines[#lines + 1] = "      • " .. desc
            else
                lines[#lines + 1] = string.format("    %-" .. pad .. "s %s", lhs, desc)
            end
        end
        lines[#lines + 1] = ""
    end
    return lines
end

function M.open()
    local lines = build_lines()

    local width = 0
    for _, l in ipairs(lines) do
        width = math.max(width, vim.fn.strdisplaywidth(l))
    end
    width = math.min(width + 2, vim.o.columns - 4)
    local height = math.min(#lines, vim.o.lines - 4)

    local buf = vim.api.nvim_create_buf(false, true)
    vim.api.nvim_buf_set_lines(buf, 0, -1, false, lines)
    vim.bo[buf].modifiable = false
    vim.bo[buf].buftype = "nofile"
    vim.bo[buf].filetype = "help"

    local win = vim.api.nvim_open_win(buf, true, {
        relative = "editor",
        width = width,
        height = height,
        row = math.floor((vim.o.lines - height) / 2) - 1,
        col = math.floor((vim.o.columns - width) / 2),
        style = "minimal",
        border = "rounded",
        title = " keybinds ",
        title_pos = "center",
    })
    vim.wo[win].cursorline = true

    local close = function()
        if vim.api.nvim_win_is_valid(win) then
            vim.api.nvim_win_close(win, true)
        end
    end
    for _, key in ipairs({ "q", "<Esc>" }) do
        vim.keymap.set("n", key, close, { buffer = buf, nowait = true, silent = true })
    end
end

vim.keymap.set("n", "<leader>H", M.open, { desc = "Open keybind cheatsheet" })

return M
