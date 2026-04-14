import type { Metadata } from "next";

export const DEFAULT_SITE_TITLE = "Bifrost Console";

const sanitizeSiteTitle = (siteTitle?: string) => {
	const trimmedTitle = siteTitle?.trim();
	return trimmedTitle ? trimmedTitle : DEFAULT_SITE_TITLE;
};

export const getSiteTitle = (siteTitle = process.env.NEXT_PUBLIC_SITE_TITLE) => sanitizeSiteTitle(siteTitle);

export const siteMetadata: Metadata = {
	title: {
		default: getSiteTitle(),
		template: `%s | ${getSiteTitle()}`,
	},
	applicationName: getSiteTitle(),
};
