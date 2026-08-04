#!/usr/bin/env bun
/**
 * zen-diagram — themed mermaid renderer.
 *
 *   zen-diagram render arch.mmd -f png
 *   zen-diagram watch arch.mmd
 *   zen-diagram new arch --template rich
 *   zen-diagram themes
 */

import { type FSWatcher, watch } from 'node:fs';
import { copyFile, readdir } from 'node:fs/promises';
import { basename, dirname, extname, join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import type { Browser } from 'puppeteer';

import {
    launchBrowser,
    type OutputFormat,
    type RenderResult,
    renderFile,
} from './render.ts';
import {
    listThemes,
    PKG_ROOT,
    type ResolvedTheme,
    resolveTheme,
    themeBackground,
} from './theme.ts';

const VERSION = '0.1.0';
const TEMPLATES_DIR = join(PKG_ROOT, 'templates');
const FORMATS: OutputFormat[] = ['svg', 'png', 'pdf'];
const SOURCE_EXTS = ['.mmd', '.mermaid'];
const DEBOUNCE_MS = 120;

/* ── terminal ──────────────────────────────────────────────────────────── */

const useColor = process.stdout.isTTY === true && !process.env.NO_COLOR;
const paint = (code: string) => (text: string) =>
    useColor ? `\x1b[${code}m${text}\x1b[0m` : text;

const dim = paint('2');
const bold = paint('1');
const blue = paint('38;5;111');
const green = paint('38;5;114');
const yellow = paint('38;5;179');
const red = paint('38;5;210');
const mauve = paint('38;5;183');

function info(message: string): void {
    console.log(message);
}

function warn(message: string): void {
    console.warn(`${yellow('warn')} ${message}`);
}

function fail(message: string): never {
    console.error(`${red('error')} ${message}`);
    process.exit(1);
}

function humanSize(bytes: number): string {
    if (bytes < 1024) return `${bytes}B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)}KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}

function errorMessage(error: unknown): string {
    return error instanceof Error ? error.message : String(error);
}

/* ── options ───────────────────────────────────────────────────────────── */

const HELP = `${bold('zen-diagram')} ${dim(`v${VERSION}`)} — themed mermaid renderer

${bold('USAGE')}
  zen-diagram [render] <file...> [options]
  zen-diagram watch <file> [options]
  zen-diagram new <name> [--template <name>]
  zen-diagram themes

${bold('OPTIONS')}
  -o, --out <path>       output file, or a directory for multiple inputs
      --out-dir <dir>    write outputs into <dir>, keeping base names
  -f, --format <fmt>     svg | png | pdf            ${dim('(default: svg)')}
  -t, --theme <name>     built-in theme or path to a config .json  ${dim('(default: zen)')}
  -b, --background <c>   background colour, or "transparent"
  -w, --width <px>       render viewport width      ${dim('(default: 1400)')}
      --height <px>      render viewport height     ${dim('(default: 900)')}
  -s, --scale <n>        device pixel ratio for png ${dim('(default: 2)')}
      --css <file>       extra CSS appended to the theme
      --config <file>    extra mermaid config merged over the theme
      --pdf-fit          size the PDF page to the diagram
      --no-project       ignore .zen-diagram.json / .zen-diagram.css
  -h, --help             show this help
  -v, --version          show version

${bold('EMBEDDING SCREENSHOTS AND HTML')}
  Local assets are inlined as data URIs, so output is self-contained.

  ${mauve('@img(./shot.png)')}                  screenshot, class ${dim("'zen-shot'")}
  ${mauve('@icon(./logo.svg)')}                 inline icon, class ${dim("'zen-icon'")}
  ${mauve('@html(./card.html, title=Auth)')}    an HTML partial, {{title}} substituted
  ${mauve("<img src='./shot.png'>")}            plain HTML also works

  Keep label HTML on one line and use single quotes — a double quote ends
  the mermaid label. Style it with the .zen-* classes in themes/zen.css.

${bold('PER-REPO CONFIG')}
  A .zen-diagram.json or .zen-diagram.css beside (or above) the diagram is
  merged over the theme automatically — no flags needed.
`;

const { values: flags, positionals } = (() => {
    try {
        return parseArgs({
            args: Bun.argv.slice(2),
            allowPositionals: true,
            options: {
                out: { type: 'string', short: 'o' },
                'out-dir': { type: 'string' },
                format: { type: 'string', short: 'f' },
                theme: { type: 'string', short: 't' },
                background: { type: 'string', short: 'b' },
                width: { type: 'string', short: 'w' },
                height: { type: 'string' },
                scale: { type: 'string', short: 's' },
                css: { type: 'string' },
                config: { type: 'string' },
                template: { type: 'string' },
                'pdf-fit': { type: 'boolean' },
                'no-project': { type: 'boolean' },
                help: { type: 'boolean', short: 'h' },
                version: { type: 'boolean', short: 'v' },
            },
        });
    } catch (error) {
        return fail(errorMessage(error));
    }
})();

function numberFlag(raw: string | undefined, label: string, fallback: number) {
    if (raw === undefined) return fallback;
    const value = Number(raw);
    if (!Number.isFinite(value) || value <= 0) {
        fail(`--${label} must be a positive number, got "${raw}"`);
    }
    return value;
}

function resolveFormat(outPath: string | undefined): OutputFormat {
    // `--out` may name a directory, which has no extension to infer from.
    const fromOut = outPath ? extname(outPath).slice(1) : '';
    const raw = flags.format ?? fromOut;
    const format = (raw || 'svg').toLowerCase();
    if (!FORMATS.includes(format as OutputFormat)) {
        fail(`unsupported format "${raw}" — expected ${FORMATS.join(', ')}`);
    }
    return format as OutputFormat;
}

async function isDirectory(path: string): Promise<boolean> {
    try {
        return (await Bun.file(path).stat()).isDirectory();
    } catch {
        return false;
    }
}

/** Where a given input should be written, honouring --out / --out-dir. */
async function outputPathFor(
    input: string,
    format: OutputFormat,
    inputCount: number,
): Promise<string> {
    const name = `${basename(input, extname(input))}.${format}`;

    if (flags['out-dir']) return resolve(flags['out-dir'], name);

    if (flags.out) {
        const out = resolve(flags.out);
        if (inputCount > 1 || (await isDirectory(out))) return join(out, name);
        return out;
    }

    return join(dirname(resolve(input)), name);
}

/* ── commands ──────────────────────────────────────────────────────────── */

interface Session {
    browser: Browser;
    theme: ResolvedTheme;
    background: string;
    format: OutputFormat;
    width: number;
    height: number;
    scale: number;
}

async function startSession(inputs: string[]): Promise<Session> {
    const first = inputs[0];
    if (first === undefined) fail('no input file given');

    const theme = await resolveTheme({
        theme: flags.theme ?? 'zen',
        fromDir: dirname(resolve(first)),
        configPath: flags.config,
        cssPath: flags.css,
        noProject: flags['no-project'],
    }).catch((error: unknown) => fail(errorMessage(error)));

    return {
        browser: await launchBrowser().catch((error: unknown) =>
            fail(errorMessage(error)),
        ),
        theme,
        background: flags.background ?? themeBackground(theme.config),
        format: resolveFormat(flags.out),
        width: numberFlag(flags.width, 'width', 1400),
        height: numberFlag(flags.height, 'height', 900),
        scale: numberFlag(flags.scale, 'scale', 2),
    };
}

async function renderOne(
    session: Session,
    input: string,
    output: string,
): Promise<RenderResult> {
    return await renderFile({
        browser: session.browser,
        input,
        output,
        format: session.format,
        theme: session.theme,
        background: session.background,
        width: session.width,
        height: session.height,
        scale: session.scale,
        pdfFit: flags['pdf-fit'],
        onWarn: warn,
    });
}

function reportRender(input: string, result: RenderResult): void {
    const rel = (path: string) => path.replace(`${process.cwd()}/`, '');
    info(
        `${green('✓')} ${rel(input)} ${dim('→')} ${blue(rel(result.output))} ` +
            dim(`(${humanSize(result.bytes)}, ${result.ms}ms)`),
    );
}

async function cmdRender(inputs: string[]): Promise<void> {
    const session = await startSession(inputs);
    let failures = 0;

    try {
        for (const input of inputs) {
            const output = await outputPathFor(
                input,
                session.format,
                inputs.length,
            );
            try {
                reportRender(input, await renderOne(session, input, output));
            } catch (error) {
                failures += 1;
                console.error(`${red('✗')} ${input}\n  ${errorMessage(error)}`);
            }
        }
    } finally {
        await session.browser.close();
    }

    if (failures > 0) process.exit(1);
}

async function cmdWatch(inputs: string[]): Promise<void> {
    if (inputs.length !== 1) fail('watch takes exactly one file');
    const input = inputs[0] as string;

    const session = await startSession(inputs);
    const output = await outputPathFor(input, session.format, 1);

    let watchers: FSWatcher[] = [];
    let timer: ReturnType<typeof setTimeout> | null = null;
    let running = false;
    // A save that lands mid-render would otherwise be missed entirely.
    let pending = false;

    let deps: string[] = [input, ...session.theme.sources];
    let stamps = new Map<string, string>();

    /**
     * mtime+size per dependency. Watch events alone can't say what actually
     * changed — see `arm` — so a rebuild is gated on one of these moving.
     */
    const stamp = async (paths: string[]): Promise<Map<string, string>> => {
        const entries = await Promise.all(
            paths.map(async (path): Promise<[string, string]> => {
                try {
                    const info = await Bun.file(path).stat();
                    return [path, `${info.mtimeMs}:${info.size}`];
                } catch {
                    return [path, 'missing'];
                }
            }),
        );
        return new Map(entries);
    };

    const changed = (
        before: Map<string, string>,
        after: Map<string, string>,
    ): boolean => {
        if (before.size !== after.size) return true;
        for (const [path, value] of after) {
            if (before.get(path) !== value) return true;
        }
        return false;
    };

    const build = async (): Promise<void> => {
        if (running) {
            pending = true;
            return;
        }
        running = true;
        try {
            const result = await renderOne(session, input, output);
            reportRender(input, result);
            deps = result.deps;
        } catch (error) {
            console.error(`${red('✗')} ${errorMessage(error)}`);
            // Keep watching the source even when the render blew up.
            deps = [input, ...session.theme.sources];
        } finally {
            stamps = await stamp(deps);
            arm(deps);
            running = false;
            if (pending) {
                pending = false;
                void build();
            }
        }
    };

    /** Rebuild only if a dependency really moved. */
    const tick = async (): Promise<void> => {
        if (running) {
            pending = true;
            return;
        }
        if (!changed(stamps, await stamp(deps))) return;
        await build();
    };

    const schedule = (): void => {
        if (timer) clearTimeout(timer);
        timer = setTimeout(() => void tick(), DEBOUNCE_MS);
    };

    /**
     * Watch the *directories* of every dependency, and treat every event in
     * them as "something might have changed" rather than matching the reported
     * filename.
     *
     * Two reasons the filename can't be trusted. Editors that save by writing a
     * temp file and renaming it over the original (neovim's default) replace the
     * inode, so a per-file watch would be orphaned. And on a rename bun reports
     * the *source* name — `tmp.mmd`, never the `t.mmd` we care about — so a name
     * filter drops the event entirely.
     *
     * Watching whole directories means the render's own output (usually written
     * beside the source) also wakes us; the mtime check in `tick` is what stops
     * that from becoming a rebuild loop.
     */
    function arm(current: string[]): void {
        for (const watcher of watchers) watcher.close();
        watchers = [];

        const dirs = new Set(current.map((dep) => dirname(resolve(dep))));
        for (const dir of dirs) {
            try {
                watchers.push(watch(dir, () => schedule()));
            } catch {
                warn(`could not watch ${dir}`);
            }
        }
    }

    const shutdown = async (): Promise<void> => {
        for (const watcher of watchers) watcher.close();
        await session.browser.close();
        process.exit(0);
    };
    process.on('SIGINT', () => void shutdown());
    process.on('SIGTERM', () => void shutdown());

    info(
        `${mauve('watching')} ${input} ${dim(`[theme: ${session.theme.name}, format: ${session.format}]`)}  ${dim('ctrl-c to stop')}`,
    );
    await build();
}

async function cmdNew(name: string | undefined): Promise<void> {
    if (!name) fail('usage: zen-diagram new <name> [--template <name>]');

    const template = flags.template ?? 'rich';
    const templatePath = join(TEMPLATES_DIR, `${template}.mmd`);
    if (!(await Bun.file(templatePath).exists())) {
        const available = (await readdir(TEMPLATES_DIR))
            .filter((entry) => extname(entry) === '.mmd')
            .map((entry) => basename(entry, '.mmd'))
            .join(', ');
        fail(`unknown template "${template}" — available: ${available}`);
    }

    const target = resolve(
        SOURCE_EXTS.includes(extname(name).toLowerCase())
            ? name
            : `${name}.mmd`,
    );
    if (await Bun.file(target).exists()) {
        fail(`${target} already exists`);
    }

    await copyFile(templatePath, target);
    info(`${green('✓')} created ${blue(target)} ${dim(`(${template})`)}`);
    info(dim(`  next: zen-diagram watch ${basename(target)}`));
}

async function cmdThemes(): Promise<void> {
    const themes = await listThemes();
    info(bold('built-in themes'));
    for (const theme of themes) {
        info(`  ${blue(theme)} ${dim(join('themes', `${theme}.json`))}`);
    }
    info('');
    info(dim('use: zen-diagram render x.mmd -t zen-light'));
    info(dim('or point -t at your own config: -t ./my-theme.json'));
}

/* ── entry ─────────────────────────────────────────────────────────────── */

async function main(): Promise<void> {
    if (flags.version) {
        info(VERSION);
        return;
    }

    const [head, ...rest] = positionals;

    if (flags.help || head === 'help' || head === undefined) {
        info(HELP);
        return;
    }

    switch (head) {
        case 'render':
            if (rest.length === 0) fail('render needs at least one input file');
            return await cmdRender(rest);
        case 'watch':
            return await cmdWatch(rest);
        case 'new':
            return await cmdNew(rest[0]);
        case 'themes':
            return await cmdThemes();
        default:
            // Bare `zen-diagram foo.mmd` is the common case; treat it as render.
            return await cmdRender(positionals);
    }
}

try {
    await main();
} catch (error) {
    fail(errorMessage(error));
}
