/**
 * Preprocessor: makes screenshots and custom HTML actually work.
 *
 * mermaid-cli renders inside its own bundled `index.html` loaded over `file://`,
 * so a relative `src="./shot.png"` in a label resolves against the *package*
 * directory and silently 404s. Every local asset therefore has to be inlined as
 * a data URI before the definition reaches mermaid. That also makes the emitted
 * SVG self-contained, so it survives being moved into docs/ or a README.
 *
 * Handled forms:
 *   <img src='./shot.png'>          raw HTML labels
 *   A@{ img: "./shot.png" }         mermaid v11 image shapes
 *   style='background: url(./x)'    inline CSS
 *   @img(./shot.png, class=…)       shorthand -> <img class='zen-shot'>
 *   @icon(./logo.svg, …)            shorthand -> <img class='zen-icon'>
 *   @html(./card.html, title=Foo)   inline an HTML partial, {{key}} substituted
 */

import { dirname, extname, isAbsolute, relative, resolve } from 'node:path';

const MIME: Record<string, string> = {
    '.png': 'image/png',
    '.jpg': 'image/jpeg',
    '.jpeg': 'image/jpeg',
    '.gif': 'image/gif',
    '.webp': 'image/webp',
    '.avif': 'image/avif',
    '.bmp': 'image/bmp',
    '.ico': 'image/x-icon',
    '.svg': 'image/svg+xml',
};

/** Assets above this inline to a very large diagram file; worth a nudge. */
const BIG_ASSET_BYTES = 1_000_000;

/** Nesting guard for @html partials that include each other. */
const MAX_INCLUDE_DEPTH = 8;

/** A whole-line mermaid comment. */
const COMMENT_LINE = /^[ \t]*%%.*$/gm;

/**
 * Placeholder for a masked comment. Uses NUL, which cannot occur in a mermaid
 * source file, so it can never collide with real content.
 */
const maskToken = (index: number) => `\u0000zdc${index}\u0000`;
// biome-ignore lint/suspicious/noControlCharactersInRegex: the NUL delimiter is the point — it makes the placeholder impossible to collide with.
const MASK_TOKEN = /\u0000zdc(\d+)\u0000/g;

export interface PreprocessResult {
    /** The definition with every local asset inlined. */
    text: string;
    /** Every file read while expanding — used to decide what `watch` watches. */
    deps: string[];
    warnings: string[];
}

interface Ctx {
    deps: Set<string>;
    warnings: string[];
    /** Cache so the same screenshot used in ten nodes is read and encoded once. */
    cache: Map<string, string | null>;
    cwd: string;
}

function isExternal(src: string): boolean {
    return /^(data:|https?:|#)/i.test(src.trim());
}

/**
 * Read a local asset and return a data URI, or null if it is missing/unreadable.
 * Anything already remote or inline is passed through untouched by the callers.
 */
async function toDataUri(
    src: string,
    baseDir: string,
    ctx: Ctx,
): Promise<string | null> {
    const raw = src.trim();
    const path = isAbsolute(raw) ? raw : resolve(baseDir, raw);
    const cached = ctx.cache.get(path);
    if (cached !== undefined) return cached;

    const file = Bun.file(path);
    if (!(await file.exists())) {
        ctx.warnings.push(`asset not found: ${relative(ctx.cwd, path)}`);
        ctx.cache.set(path, null);
        return null;
    }

    const bytes = new Uint8Array(await file.arrayBuffer());
    const ext = extname(path).toLowerCase();
    const mime = MIME[ext] ?? file.type ?? 'application/octet-stream';
    if (bytes.byteLength > BIG_ASSET_BYTES) {
        const mb = (bytes.byteLength / 1_000_000).toFixed(1);
        ctx.warnings.push(
            `${relative(ctx.cwd, path)} is ${mb}MB — it gets base64-inlined, consider shrinking it`,
        );
    }

    const uri = `data:${mime};base64,${Buffer.from(bytes).toString('base64')}`;
    ctx.deps.add(path);
    ctx.cache.set(path, uri);
    return uri;
}

/**
 * Split a directive argument list on top-level commas, honouring quoted values
 * so `title='a, b'` stays one argument.
 */
function splitArgs(input: string): string[] {
    const out: string[] = [];
    let current = '';
    let quote: string | null = null;
    for (const ch of input) {
        if (quote) {
            if (ch === quote) quote = null;
            else current += ch;
            continue;
        }
        if (ch === '"' || ch === "'") {
            quote = ch;
            continue;
        }
        if (ch === ',') {
            out.push(current.trim());
            current = '';
            continue;
        }
        current += ch;
    }
    out.push(current.trim());
    return out.filter((part) => part.length > 0);
}

function parseDirectiveArgs(input: string): {
    path: string;
    attrs: Record<string, string>;
} {
    const [first = '', ...rest] = splitArgs(input);
    const attrs: Record<string, string> = {};
    for (const part of rest) {
        const eq = part.indexOf('=');
        if (eq === -1) continue;
        attrs[part.slice(0, eq).trim()] = part.slice(eq + 1).trim();
    }
    return { path: first, attrs };
}

/**
 * Mermaid ends a quoted label at the first `"`, so any HTML we generate or
 * inline has to use single quotes, and has to stay on one line.
 */
function labelSafe(html: string): string {
    return html
        .replace(/"/g, "'")
        .replace(/\s*\r?\n\s*/g, ' ')
        .trim();
}

function attrString(attrs: Record<string, string>): string {
    return Object.entries(attrs)
        .map(([key, value]) => ` ${key}='${value.replace(/'/g, '&#39;')}'`)
        .join('');
}

/** Replace every regex match using an async replacer. */
async function replaceAsync(
    input: string,
    pattern: RegExp,
    replacer: (match: RegExpExecArray) => Promise<string>,
): Promise<string> {
    const matches = [...input.matchAll(pattern)];
    if (matches.length === 0) return input;

    let out = '';
    let last = 0;
    for (const match of matches) {
        const start = match.index ?? 0;
        out += input.slice(last, start);
        out += await replacer(match as RegExpExecArray);
        last = start + match[0].length;
    }
    return out + input.slice(last);
}

/** Inline `<img src>`, `img:` shape metadata and CSS `url()` within a fragment. */
async function inlineAssets(
    text: string,
    baseDir: string,
    ctx: Ctx,
): Promise<string> {
    // <img src='…'> / <image href='…'>
    let out = await replaceAsync(
        text,
        /(<(?:img|image)\b[^>]*?\b(?:src|href|xlink:href)\s*=\s*)(['"])(.*?)\2/gi,
        async (m) => {
            const [, head = '', quote = "'", src = ''] = m;
            if (isExternal(src)) return m[0];
            const uri = await toDataUri(src, baseDir, ctx);
            return uri ? `${head}${quote}${uri}${quote}` : m[0];
        },
    );

    // mermaid v11 image shapes: A@{ img: "./shot.png", w: 200 }
    out = await replaceAsync(out, /@\{[\s\S]*?\}/g, async (block) => {
        return await replaceAsync(
            block[0],
            /(\bimg\s*:\s*)(['"])(.*?)\2/g,
            async (m) => {
                const [, head = '', quote = '"', src = ''] = m;
                if (isExternal(src)) return m[0];
                const uri = await toDataUri(src, baseDir, ctx);
                return uri ? `${head}${quote}${uri}${quote}` : m[0];
            },
        );
    });

    // CSS url(…) inside inline styles
    out = await replaceAsync(
        out,
        /url\(\s*(['"]?)([^'")]+)\1\s*\)/gi,
        async (m) => {
            const [, quote = '', src = ''] = m;
            if (isExternal(src)) return m[0];
            const uri = await toDataUri(src, baseDir, ctx);
            return uri ? `url(${quote}${uri}${quote})` : m[0];
        },
    );

    return out;
}

/** Expand @img / @icon / @html directives, recursing into included partials. */
async function expandDirectives(
    text: string,
    baseDir: string,
    ctx: Ctx,
    depth: number,
): Promise<string> {
    if (depth > MAX_INCLUDE_DEPTH) {
        ctx.warnings.push(
            `@html include nesting exceeded ${MAX_INCLUDE_DEPTH} levels — stopped expanding`,
        );
        return text;
    }

    // @img(path, key=value…) and @icon(path, key=value…)
    let out = await replaceAsync(text, /@(img|icon)\(([^)]*)\)/g, async (m) => {
        const [, kind = 'img', argsRaw = ''] = m;
        const { path, attrs } = parseDirectiveArgs(argsRaw);
        if (!path) return m[0];
        const uri = isExternal(path)
            ? path
            : await toDataUri(path, baseDir, ctx);
        if (!uri) return m[0];
        const merged: Record<string, string> = {
            class: kind === 'icon' ? 'zen-icon' : 'zen-shot',
            ...attrs,
        };
        return `<img src='${uri}'${attrString(merged)}/>`;
    });

    // @html(path, key=value…) — inline a partial, substituting {{key}}
    out = await replaceAsync(out, /@html\(([^)]*)\)/g, async (m) => {
        const [, argsRaw = ''] = m;
        const { path, attrs } = parseDirectiveArgs(argsRaw);
        if (!path) return m[0];

        const resolved = isAbsolute(path) ? path : resolve(baseDir, path);
        const file = Bun.file(resolved);
        if (!(await file.exists())) {
            ctx.warnings.push(
                `@html partial not found: ${relative(ctx.cwd, resolved)}`,
            );
            return m[0];
        }
        ctx.deps.add(resolved);

        // Comments never belong in a label, and a directive quoted inside one
        // would otherwise be expanded against the partial's own directory.
        let html = (await file.text()).replace(/<!--[\s\S]*?-->/g, '');
        for (const [key, value] of Object.entries(attrs)) {
            html = html.replaceAll(`{{${key}}}`, value);
        }
        // Partials resolve their own relative paths against their own directory.
        const partialDir = dirname(resolved);
        html = await expandDirectives(html, partialDir, ctx, depth + 1);
        html = await inlineAssets(html, partialDir, ctx);
        return labelSafe(html);
    });

    return out;
}

/**
 * Expand directives and inline every local asset referenced by `text`.
 *
 * @param text  Raw mermaid definition.
 * @param file  Path of the source file; relative assets resolve against its dir.
 */
export async function preprocess(
    text: string,
    file: string,
    cwd = process.cwd(),
): Promise<PreprocessResult> {
    const ctx: Ctx = {
        deps: new Set(),
        warnings: [],
        cache: new Map(),
        cwd,
    };
    const baseDir = dirname(resolve(file));

    // Hide `%%` comment lines from the expansion passes. A commented-out
    // @img/@html line is one the author switched off on purpose — expanding it
    // would read files they never asked for and warn about the ones missing.
    const comments: string[] = [];
    const masked = text.replace(COMMENT_LINE, (line) => {
        comments.push(line);
        return maskToken(comments.length - 1);
    });

    let out = await expandDirectives(masked, baseDir, ctx, 0);
    out = await inlineAssets(out, baseDir, ctx);
    out = out.replace(
        MASK_TOKEN,
        (_match, index: string) => comments[Number(index)] ?? '',
    );

    return {
        text: out,
        deps: [...ctx.deps],
        warnings: ctx.warnings,
    };
}
