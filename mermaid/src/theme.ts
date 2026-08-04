/**
 * Theme resolution.
 *
 * A theme is a pair of sibling files: `<name>.json` (mermaid config, including
 * `themeVariables`) and an optional `<name>.css` (injected into the render page,
 * which is what styles custom HTML in labels).
 *
 * Layering, lowest precedence first:
 *   1. the named/built-in theme
 *   2. `.zen-diagram.json` / `.zen-diagram.css` found by walking up from the
 *      diagram's directory — per-repo overrides that need no flags
 *   3. explicit `--config` / `--css`
 */

import { realpathSync } from 'node:fs';
import { readdir } from 'node:fs/promises';
import { basename, dirname, extname, join, parse, resolve } from 'node:path';

export const PKG_ROOT = resolve(realpathSync(import.meta.dir), '..');
export const THEMES_DIR = join(PKG_ROOT, 'themes');

const PROJECT_CONFIG = '.zen-diagram.json';
const PROJECT_CSS = '.zen-diagram.css';

/** Mermaid config is an open-ended nested record; keep it honestly typed. */
export type JsonValue =
    | string
    | number
    | boolean
    | null
    | JsonValue[]
    | { [key: string]: JsonValue };
export type MermaidConfig = { [key: string]: JsonValue };

export interface ResolvedTheme {
    name: string;
    config: MermaidConfig;
    css: string;
    /** Config/CSS files that fed this theme — `watch` reloads on their change. */
    sources: string[];
}

function isPlainObject(value: JsonValue | undefined): value is MermaidConfig {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Recursive merge; `override` wins, nested objects merge rather than replace. */
function deepMerge(
    base: MermaidConfig,
    override: MermaidConfig,
): MermaidConfig {
    const out: MermaidConfig = { ...base };
    for (const [key, value] of Object.entries(override)) {
        const existing = out[key];
        if (isPlainObject(existing) && isPlainObject(value)) {
            out[key] = deepMerge(existing, value);
        } else {
            out[key] = value;
        }
    }
    return out;
}

async function readJson(path: string): Promise<MermaidConfig> {
    const text = await Bun.file(path).text();
    let parsed: unknown;
    try {
        parsed = JSON.parse(text);
    } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        throw new Error(`invalid JSON in ${path}: ${reason}`);
    }
    if (!isPlainObject(parsed as JsonValue)) {
        throw new Error(`${path} must contain a JSON object`);
    }
    return parsed as MermaidConfig;
}

async function readIfExists(path: string): Promise<string | null> {
    const file = Bun.file(path);
    return (await file.exists()) ? await file.text() : null;
}

/** Built-in theme names, derived from the *.json files in themes/. */
export async function listThemes(): Promise<string[]> {
    const entries = await readdir(THEMES_DIR);
    return entries
        .filter((entry) => extname(entry) === '.json')
        .map((entry) => basename(entry, '.json'))
        .sort();
}

/**
 * Walk up from `startDir` to the filesystem root collecting project overrides.
 * Returned nearest-last so closer files win when merged in order.
 */
async function collectProjectOverrides(
    startDir: string,
): Promise<{ configs: string[]; csses: string[] }> {
    const configs: string[] = [];
    const csses: string[] = [];
    let dir = resolve(startDir);

    for (;;) {
        const configPath = join(dir, PROJECT_CONFIG);
        const cssPath = join(dir, PROJECT_CSS);
        if (await Bun.file(configPath).exists()) configs.unshift(configPath);
        if (await Bun.file(cssPath).exists()) csses.unshift(cssPath);

        const parent = dirname(dir);
        if (parent === dir) break;
        dir = parent;
    }
    return { configs, csses };
}

export interface ThemeOptions {
    /** Built-in theme name, or a path to a config JSON. */
    theme: string;
    /** Directory to start the project-override search from. */
    fromDir: string;
    /** Extra config file merged last but one. */
    configPath?: string | undefined;
    /** Extra CSS file appended last. */
    cssPath?: string | undefined;
    /** Skip the `.zen-diagram.*` walk-up. */
    noProject?: boolean | undefined;
}

export async function resolveTheme(
    options: ThemeOptions,
): Promise<ResolvedTheme> {
    const { theme, fromDir } = options;
    const sources: string[] = [];

    // A theme is either a built-in name or a path to a JSON config.
    const looksLikePath =
        theme.includes('/') || extname(theme).toLowerCase() === '.json';
    const configPath = looksLikePath
        ? resolve(theme)
        : join(THEMES_DIR, `${theme}.json`);

    if (!(await Bun.file(configPath).exists())) {
        const available = (await listThemes()).join(', ');
        throw new Error(
            `unknown theme "${theme}" — no such file ${configPath}\navailable: ${available}`,
        );
    }

    let config = await readJson(configPath);
    sources.push(configPath);

    // Sibling stylesheet, e.g. themes/zen.json -> themes/zen.css
    const parsed = parse(configPath);
    const themeCssPath = join(parsed.dir, `${parsed.name}.css`);
    let css = (await readIfExists(themeCssPath)) ?? '';
    if (css) sources.push(themeCssPath);

    if (!options.noProject) {
        const overrides = await collectProjectOverrides(fromDir);
        for (const path of overrides.configs) {
            config = deepMerge(config, await readJson(path));
            sources.push(path);
        }
        for (const path of overrides.csses) {
            css += `\n\n/* ${path} */\n${await Bun.file(path).text()}`;
            sources.push(path);
        }
    }

    if (options.configPath) {
        const path = resolve(options.configPath);
        config = deepMerge(config, await readJson(path));
        sources.push(path);
    }

    if (options.cssPath) {
        const path = resolve(options.cssPath);
        const extra = await readIfExists(path);
        if (extra === null) throw new Error(`css file not found: ${path}`);
        css += `\n\n/* ${path} */\n${extra}`;
        sources.push(path);
    }

    return {
        name: looksLikePath ? parsed.name : theme,
        config,
        css,
        sources,
    };
}

/** The theme's own background, used as the default for opaque PNG/PDF output. */
export function themeBackground(config: MermaidConfig): string {
    const vars = config.themeVariables;
    if (isPlainObject(vars) && typeof vars.background === 'string') {
        return vars.background;
    }
    return 'white';
}
