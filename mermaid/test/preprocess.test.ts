import { describe, expect, test } from 'bun:test';
import { join } from 'node:path';

import { preprocess } from '../src/preprocess.ts';
import { chromeCandidates } from '../src/render.ts';

const FIXTURES = join(import.meta.dir, 'fixtures');
/** Any path in this dir; only its dirname is used to resolve relative assets. */
const SOURCE = join(FIXTURES, 'diagram.mmd');

const PNG_PREFIX = 'data:image/png;base64,';

async function run(text: string) {
    return await preprocess(text, SOURCE, FIXTURES);
}

describe('asset inlining', () => {
    test('inlines a local <img src> as a data URI', async () => {
        const { text, deps, warnings } = await run(
            `flowchart LR\n    A["<img src='./dot.png'/>"]\n`,
        );
        expect(text).toContain(PNG_PREFIX);
        expect(text).not.toContain('./dot.png');
        expect(warnings).toEqual([]);
        expect(deps).toEqual([join(FIXTURES, 'dot.png')]);
    });

    test('leaves remote and already-inlined sources alone', async () => {
        const source = `flowchart LR\n    A["<img src='https://x.test/a.png'/>"]\n    B["<img src='data:image/png;base64,AAAA'/>"]\n`;
        const { text, deps } = await run(source);
        expect(text).toBe(source);
        expect(deps).toEqual([]);
    });

    test('inlines mermaid v11 image shapes', async () => {
        const { text } = await run(
            `flowchart LR\n    A@{ img: "./dot.png", w: 60 }\n`,
        );
        expect(text).toContain(PNG_PREFIX);
    });

    test('inlines CSS url() in inline styles', async () => {
        const { text } = await run(
            `flowchart LR\n    A["<div style='background: url(./dot.png)'></div>"]\n`,
        );
        expect(text).toContain(`url(${PNG_PREFIX}`);
    });

    test('reads a repeated asset once', async () => {
        const { deps } = await run(
            `flowchart LR\n    A["<img src='./dot.png'/>"]\n    B["@img(./dot.png)"]\n`,
        );
        expect(deps).toEqual([join(FIXTURES, 'dot.png')]);
    });

    test('warns and leaves the source untouched when an asset is missing', async () => {
        const { text, warnings } = await run(
            `flowchart LR\n    A["<img src='./nope.png'/>"]\n`,
        );
        expect(text).toContain('./nope.png');
        expect(warnings).toHaveLength(1);
        expect(warnings[0]).toContain('nope.png');
    });
});

describe('directives', () => {
    test('@img expands with the default screenshot class', async () => {
        const { text } = await run(`flowchart LR\n    A["@img(./dot.png)"]\n`);
        expect(text).toContain(`<img src='${PNG_PREFIX}`);
        expect(text).toContain("class='zen-shot'");
    });

    test('@icon expands with the icon class', async () => {
        const { text } = await run(`flowchart LR\n    A["@icon(./dot.png)"]\n`);
        expect(text).toContain("class='zen-icon'");
    });

    test('@img attributes override the default class', async () => {
        const { text } = await run(
            `flowchart LR\n    A["@img(./dot.png, class=zen-shot lg, alt=a shot)"]\n`,
        );
        expect(text).toContain("class='zen-shot lg'");
        expect(text).toContain("alt='a shot'");
    });

    test('@html inlines a partial, substitutes {{keys}} and inlines its assets', async () => {
        const { text, deps } = await run(
            `flowchart LR\n    A["@html(./badge.html, title=Auth)"]\n`,
        );
        expect(text).toContain('Auth');
        expect(text).toContain(PNG_PREFIX);
        expect(deps).toContain(join(FIXTURES, 'badge.html'));
        // The partial's own relative path resolves against the partial's dir.
        expect(deps).toContain(join(FIXTURES, 'dot.png'));
    });

    test('@html output is safe to sit inside a mermaid label', async () => {
        const { text } = await run(
            `flowchart LR\n    A["@html(./badge.html, title=Auth)"]\n`,
        );
        const label = text.split('A["')[1]?.split('"]')[0] ?? '';
        expect(label).not.toContain('"');
        expect(label).not.toContain('\n');
        expect(label).not.toContain('<!--');
    });

    test('a quoted value may contain commas', async () => {
        const { text } = await run(
            `flowchart LR\n    A["@img(./dot.png, alt='one, two')"]\n`,
        );
        expect(text).toContain("alt='one, two'");
    });

    test('a missing partial warns and leaves the directive in place', async () => {
        const { text, warnings } = await run(
            `flowchart LR\n    A["@html(./nope.html)"]\n`,
        );
        expect(text).toContain('@html(./nope.html)');
        expect(warnings).toHaveLength(1);
    });
});

describe('comments', () => {
    test('directives inside %% comments are left alone', async () => {
        const source = `%% example: A["@img(./dot.png)"]\n%% and @html(./badge.html)\nflowchart LR\n    A[plain]\n`;
        const { text, warnings, deps } = await run(source);
        expect(text).toBe(source);
        expect(warnings).toEqual([]);
        expect(deps).toEqual([]);
    });

    test('a commented-out missing asset does not warn', async () => {
        const { warnings } = await run(
            `%%   B["@img(./nope.png)"]\nflowchart LR\n    A[plain]\n`,
        );
        expect(warnings).toEqual([]);
    });

    test('comments are restored verbatim around expanded content', async () => {
        const { text } = await run(
            `%% keep me\nflowchart LR\n    A["@img(./dot.png)"]\n%% and me\n`,
        );
        expect(text).toContain('%% keep me');
        expect(text).toContain('%% and me');
        expect(text).toContain(PNG_PREFIX);
        // no mask placeholder leaked through
        expect(text).not.toContain('\u0000');
    });
});

describe('chrome discovery', () => {
    test('macOS candidates are .app bundle paths', () => {
        const mac = chromeCandidates('darwin');
        expect(mac.length).toBeGreaterThan(0);
        expect(mac.every((path) => path.includes('.app/Contents/MacOS/'))).toBe(
            true,
        );
        expect(mac[0]).toContain('Google Chrome');
    });

    test('linux candidates are plain unix binaries', () => {
        const linux = chromeCandidates('linux');
        expect(linux.length).toBeGreaterThan(0);
        expect(linux.every((path) => path.startsWith('/'))).toBe(true);
        expect(linux.some((path) => path.includes('.app/'))).toBe(false);
    });

    test('the two platforms do not share a path', () => {
        const mac = new Set(chromeCandidates('darwin'));
        expect(chromeCandidates('linux').some((p) => mac.has(p))).toBe(false);
    });
});
