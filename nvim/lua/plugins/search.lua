return {
	{
		"MagicDuck/grug-far.nvim",
		-- VSCode-like project-wide search & replace (regex, include/exclude globs).
		-- Powered by ripgrep. Fields in the buffer: Search, Replace, Files Filter
		-- (e.g. `*.lua` to include, `!*.test.lua` to exclude), Flags, Paths.
		cmd = { "GrugFar" },
		keys = {
			{
				"<leader>sr",
				function()
					require("grug-far").open()
				end,
				mode = { "n" },
				desc = "Search/Replace in project",
			},
			{
				"<leader>sr",
				function()
					-- open prefilled with the visual selection
					require("grug-far").with_visual_selection()
				end,
				mode = { "v" },
				desc = "Search/Replace selection in project",
			},
			{
				"<leader>sw",
				function()
					require("grug-far").open({ prefills = { search = vim.fn.expand("<cword>") } })
				end,
				desc = "Search/Replace word under cursor",
			},
		},
		opts = {
			-- search hidden files too; tweak the Flags field live in the buffer
			engines = {
				ripgrep = {
					extraArgs = "--hidden",
				},
			},
			windowCreationCommand = "botright vsplit",
		},
	},
}
