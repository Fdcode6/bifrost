import { describe, expect, it } from "vitest";

import { resolveCustomBuildId } from "./buildId";

describe("resolveCustomBuildId", () => {
	it("returns undefined when no deployment build id is provided", () => {
		expect(resolveCustomBuildId({})).toBeUndefined();
	});

	it("prefers an explicit BIFROST build id", () => {
		expect(resolveCustomBuildId({ BIFROST_BUILD_ID: "release-2026-04-14" })).toBe("release-2026-04-14");
	});

	it("falls back to commit shas from common CI environments", () => {
		expect(resolveCustomBuildId({ GITHUB_SHA: "1234567890abcdef" })).toBe("1234567890ab");
		expect(resolveCustomBuildId({ VERCEL_GIT_COMMIT_SHA: "fedcba0987654321" })).toBe("fedcba098765");
		expect(resolveCustomBuildId({ SOURCE_VERSION: "abcabcabcabc9999" })).toBe("abcabcabcabc");
	});
});
