import { afterEach, describe, expect, it, vi } from "vitest";

const originalSiteTitle = process.env.NEXT_PUBLIC_SITE_TITLE;

afterEach(() => {
	if (originalSiteTitle === undefined) {
		delete process.env.NEXT_PUBLIC_SITE_TITLE;
	} else {
		process.env.NEXT_PUBLIC_SITE_TITLE = originalSiteTitle;
	}

	vi.resetModules();
});

describe("site metadata", () => {
	it("falls back to the default site title when no custom title is configured", async () => {
		delete process.env.NEXT_PUBLIC_SITE_TITLE;
		vi.resetModules();

		const siteMetadataModule = await import("./siteMetadata");

		expect(siteMetadataModule.getSiteTitle()).toBe("Bifrost Console");
		expect(siteMetadataModule.siteMetadata.title).toEqual({
			default: "Bifrost Console",
			template: "%s | Bifrost Console",
		});
		expect(siteMetadataModule.siteMetadata.applicationName).toBe("Bifrost Console");
	});

	it("uses NEXT_PUBLIC_SITE_TITLE when it is configured", async () => {
		process.env.NEXT_PUBLIC_SITE_TITLE = "ZDFan AI Gateway";
		vi.resetModules();

		const siteMetadataModule = await import("./siteMetadata");

		expect(siteMetadataModule.getSiteTitle()).toBe("ZDFan AI Gateway");
		expect(siteMetadataModule.siteMetadata.title).toEqual({
			default: "ZDFan AI Gateway",
			template: "%s | ZDFan AI Gateway",
		});
		expect(siteMetadataModule.siteMetadata.applicationName).toBe("ZDFan AI Gateway");
	});

	it("ignores blank custom titles", async () => {
		process.env.NEXT_PUBLIC_SITE_TITLE = "   ";
		vi.resetModules();

		const siteMetadataModule = await import("./siteMetadata");

		expect(siteMetadataModule.getSiteTitle()).toBe("Bifrost Console");
	});
});
