import { fixupConfigRules } from "@eslint/compat";
import { FlatCompat } from "@eslint/eslintrc";
import js from "@eslint/js";
import typescriptEslintEslintPlugin from "@typescript-eslint/eslint-plugin";
import typescriptEslintParser from "@typescript-eslint/parser";
import eslintPluginUnusedImports from "eslint-plugin-unused-imports";
import path from "path";
import { fileURLToPath } from "url";

const FILENAME = fileURLToPath(import.meta.url);
const DIRNAME = path.dirname(FILENAME);

const compat = new FlatCompat({
	baseDirectory: DIRNAME,
	recommendedConfig: js.configs.recommended,
});

const eslintConfig = [
	...fixupConfigRules(compat.extends("next/core-web-vitals", "prettier")),
	{
		plugins: {
			"@typescript-eslint": typescriptEslintEslintPlugin,
			"unused-imports": eslintPluginUnusedImports,
		},
	},
	{
		languageOptions: {
			parser: typescriptEslintParser,
			parserOptions: {
				ecmaVersion: "latest",
				sourceType: "module",
				tsconfigRootDir: DIRNAME,
			},
		},
	},
	{
		rules: {
			"import/no-cycle": [
				"warn",
				{
					maxDepth: 1,
					ignoreExternal: true,
				},
			],
			"comma-dangle": "off",
			"@next/next/no-html-link-for-pages": ["off"],
			"@next/next/no-img-element": "off",
			"react/no-unescaped-entities": "off",
			"import/no-extraneous-dependencies": "off",
			"import/no-named-as-default": "off",
			"react/react-in-jsx-scope": "off",
			"unused-imports/no-unused-imports": "error",
			"@typescript-eslint/ban-ts-comment": ["off"],
			"@typescript-eslint/no-explicit-any": ["warn"],
			// "@typescript-eslint/no-floating-promises": "error",
			// "@typescript-eslint/no-non-null-assertion": "error",
			"@typescript-eslint/naming-convention": "off",
		},
	},
];

export default eslintConfig;
