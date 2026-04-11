import { createHighlighterCore, type HighlighterCore, type TokensResult } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";

// Languages we actually want to highlight in the Bifrost UI.
// Adding a new language requires only adding the dynamic import below.
const langLoaders = {
	typescript: () => import("shiki/langs/typescript.mjs"),
	javascript: () => import("shiki/langs/javascript.mjs"),
	tsx: () => import("shiki/langs/tsx.mjs"),
	jsx: () => import("shiki/langs/jsx.mjs"),
	json: () => import("shiki/langs/json.mjs"),
	python: () => import("shiki/langs/python.mjs"),
	go: () => import("shiki/langs/go.mjs"),
	bash: () => import("shiki/langs/bash.mjs"),
	shell: () => import("shiki/langs/shell.mjs"),
	yaml: () => import("shiki/langs/yaml.mjs"),
	sql: () => import("shiki/langs/sql.mjs"),
	html: () => import("shiki/langs/html.mjs"),
	css: () => import("shiki/langs/css.mjs"),
	markdown: () => import("shiki/langs/markdown.mjs"),
	xml: () => import("shiki/langs/xml.mjs"),
} as const;

const themeLoaders = {
	"github-light": () => import("shiki/themes/github-light.mjs"),
	"github-dark": () => import("shiki/themes/github-dark.mjs"),
} as const;

type SupportedLang = keyof typeof langLoaders;
type SupportedTheme = keyof typeof themeLoaders;

const langAliases: Record<string, SupportedLang> = {
	ts: "typescript",
	js: "javascript",
	py: "python",
	golang: "go",
	sh: "bash",
	yml: "yaml",
	htm: "html",
	md: "markdown",
};

const supportedSet = new Set(Object.keys(langLoaders) as SupportedLang[]);

const normalizeLanguage = (lang: string): SupportedLang | "text" => {
	const key = lang.trim().toLowerCase();
	const alias = langAliases[key];
	if (alias) return alias;
	if (supportedSet.has(key as SupportedLang)) return key as SupportedLang;
	return "text";
};

let highlighterPromise: Promise<HighlighterCore> | null = null;
const loadedLangs = new Set<string>();
const loadedThemes = new Set<string>();
const tokenCache = new Map<string, TokensResult>();
const pendingCallbacks = new Map<string, Set<(result: TokensResult) => void>>();

const getHighlighter = async (
	lang: SupportedLang | "text",
	themes: [SupportedTheme, SupportedTheme],
): Promise<HighlighterCore> => {
	if (!highlighterPromise) {
		highlighterPromise = createHighlighterCore({
			themes: [],
			langs: [],
			engine: createJavaScriptRegexEngine({ forgiving: true }),
		});
	}
	const highlighter = await highlighterPromise;

	for (const theme of themes) {
		if (!loadedThemes.has(theme)) {
			const mod = await themeLoaders[theme]();
			await highlighter.loadTheme(mod.default);
			loadedThemes.add(theme);
		}
	}

	if (lang !== "text" && !loadedLangs.has(lang)) {
		const mod = await langLoaders[lang]();
		await highlighter.loadLanguage(mod.default);
		loadedLangs.add(lang);
	}

	return highlighter;
};

const cacheKey = (code: string, lang: string, themes: [string, string]): string => {
	const head = code.slice(0, 100);
	const tail = code.length > 100 ? code.slice(-100) : "";
	return `${lang}:${themes[0]}:${themes[1]}:${code.length}:${head}:${tail}`;
};

interface HighlightOptions {
	code: string;
	language: string;
	themes: [string, string];
}

interface CodeHighlighterPlugin {
	name: "shiki";
	type: "code-highlighter";
	getSupportedLanguages: () => string[];
	getThemes: () => [string, string];
	supportsLanguage: (language: string) => boolean;
	highlight: (options: HighlightOptions, callback?: (result: TokensResult) => void) => TokensResult | null;
}

interface CodePluginOptions {
	themes?: [SupportedTheme, SupportedTheme];
}

export function createCodePlugin(options: CodePluginOptions = {}): CodeHighlighterPlugin {
	const themes = options.themes ?? (["github-light", "github-dark"] as [SupportedTheme, SupportedTheme]);

	return {
		name: "shiki",
		type: "code-highlighter",
		getSupportedLanguages: () => Array.from(supportedSet),
		getThemes: () => themes,
		supportsLanguage: (language: string) => normalizeLanguage(language) !== "text",
		highlight({ code, language, themes: optThemes }, callback) {
			const lang = normalizeLanguage(language);
			const key = cacheKey(code, lang, optThemes);

			const cached = tokenCache.get(key);
			if (cached) return cached;

			if (callback) {
				if (!pendingCallbacks.has(key)) pendingCallbacks.set(key, new Set());
				pendingCallbacks.get(key)!.add(callback);
			}

			const themesPair = optThemes as [SupportedTheme, SupportedTheme];
			getHighlighter(lang, themesPair)
				.then((highlighter) => {
					const finalLang = lang === "text" ? "text" : highlighter.getLoadedLanguages().includes(lang) ? lang : "text";
					const result = highlighter.codeToTokens(code, {
						lang: finalLang,
						themes: { light: optThemes[0], dark: optThemes[1] },
					});
					tokenCache.set(key, result);
					const callbacks = pendingCallbacks.get(key);
					if (callbacks) {
						for (const cb of callbacks) cb(result);
						pendingCallbacks.delete(key);
					}
				})
				.catch((err) => {
					console.error("[Bifrost Code Highlighter] Failed to highlight:", err);
					pendingCallbacks.delete(key);
				});

			return null;
		},
	};
}

export const code = createCodePlugin();
