/**
 * Rendering: browser lifecycle + one diagram -> one output file.
 *
 * The browser is expensive to start (~1s), so `watch` keeps a single instance
 * alive across rebuilds and only pays for it once.
 */

import { existsSync } from 'node:fs';
import { mkdir } from 'node:fs/promises';
import { dirname } from 'node:path';
import { renderMermaid } from '@mermaid-js/mermaid-cli';
import puppeteer, { type Browser } from 'puppeteer';

import { preprocess } from './preprocess.ts';
import type { MermaidConfig, ResolvedTheme } from './theme.ts';

export type OutputFormat = 'svg' | 'png' | 'pdf';

/** mermaid's own default; see the note in `renderFile`. */
const DEFAULT_MAX_TEXT_SIZE = 50_000;

/** Chrome is already installed system-wide; puppeteer's own download is skipped. */
const CHROME_CANDIDATES = [
    '/usr/bin/google-chrome-stable',
    '/usr/bin/google-chrome',
    '/usr/bin/chromium',
    '/usr/bin/chromium-browser',
    '/usr/bin/brave',
    '/opt/google/chrome/chrome',
];

function findChrome(): string | undefined {
    const fromEnv = process.env.PUPPETEER_EXECUTABLE_PATH;
    if (fromEnv) return fromEnv;
    return CHROME_CANDIDATES.find((path) => existsSync(path));
}

export async function launchBrowser(): Promise<Browser> {
    const executablePath = findChrome();
    try {
        return await puppeteer.launch(
            executablePath ? { executablePath } : { channel: 'chrome' },
        );
    } catch (error) {
        const reason = error instanceof Error ? error.message : String(error);
        throw new Error(
            `could not launch Chrome.\n${reason}\n` +
                'Set PUPPETEER_EXECUTABLE_PATH to a Chrome/Chromium binary.',
        );
    }
}

export interface RenderOptions {
    browser: Browser;
    /** Path to the .mmd source. */
    input: string;
    /** Path to write. */
    output: string;
    format: OutputFormat;
    theme: ResolvedTheme;
    background: string;
    width: number;
    height: number;
    scale: number;
    /** Fit a PDF page to the diagram instead of using a fixed page size. */
    pdfFit?: boolean | undefined;
    cwd?: string | undefined;
    /**
     * Called as soon as preprocessing finds a problem. Warnings are also on the
     * result, but a missing asset is usually *why* the render then fails, so it
     * has to escape before the throw.
     */
    onWarn?: ((message: string) => void) | undefined;
}

export interface RenderResult {
    output: string;
    bytes: number;
    /** Source + every asset and theme file it depends on. */
    deps: string[];
    warnings: string[];
    ms: number;
}

export async function renderFile(
    options: RenderOptions,
): Promise<RenderResult> {
    const started = performance.now();
    const cwd = options.cwd ?? process.cwd();

    const source = Bun.file(options.input);
    if (!(await source.exists())) {
        throw new Error(`no such file: ${options.input}`);
    }

    const raw = await source.text();
    if (raw.trim().length === 0) {
        throw new Error(`${options.input} is empty`);
    }

    const { text, deps, warnings } = await preprocess(raw, options.input, cwd);
    if (options.onWarn) {
        for (const message of warnings) options.onWarn(message);
    }

    // A single inlined screenshot is worth ~250KB of base64, which sails past
    // mermaid's 50k `maxTextSize` and gets rendered as "Maximum text size in
    // diagram exceeded". Raise the ceiling to fit, unless the config asks for
    // something roomier already.
    const needed = text.length + 10_000;
    const configured =
        typeof options.theme.config.maxTextSize === 'number'
            ? options.theme.config.maxTextSize
            : DEFAULT_MAX_TEXT_SIZE;

    // The theme stylesheet goes in via `themeCSS`, NOT mermaid-cli's `myCSS`.
    // `myCSS` is appended to the SVG *after* mermaid.render() has measured every
    // label, so anything affecting layout (flex, gap, image sizing, padding)
    // lands too late and the label overflows its <foreignObject> clip box —
    // which is exactly the custom-HTML case this tool exists for. `themeCSS` is
    // in the document before measurement, so labels are sized with it applied.
    const existingThemeCss =
        typeof options.theme.config.themeCSS === 'string'
            ? options.theme.config.themeCSS
            : '';
    const themeCSS = [existingThemeCss, options.theme.css]
        .filter((part) => part.length > 0)
        .join('\n');

    const config: MermaidConfig = {
        ...options.theme.config,
        ...(themeCSS ? { themeCSS } : {}),
        ...(configured < needed ? { maxTextSize: needed } : {}),
    };

    const { data } = await renderMermaid(
        options.browser,
        text,
        options.format,
        {
            backgroundColor: options.background,
            mermaidConfig: config,
            pdfFit: options.pdfFit ?? false,
            viewport: {
                width: options.width,
                height: options.height,
                deviceScaleFactor: options.scale,
            },
        },
    );

    await mkdir(dirname(options.output), { recursive: true });
    await Bun.write(options.output, data);

    return {
        output: options.output,
        bytes: data.byteLength,
        deps: [options.input, ...deps, ...options.theme.sources],
        warnings,
        ms: Math.round(performance.now() - started),
    };
}
