import { beforeAll, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/store", () => ({
	useGetCoreConfigQuery: vi.fn(),
	useGetVersionQuery: vi.fn(),
	useLogoutMutation: vi.fn(),
}));

vi.mock("@enterprise/lib", () => ({
	RbacOperation: { View: "view" },
	RbacResource: {},
	useRbac: vi.fn(),
}));

vi.mock("@enterprise/lib/store/utils/tokenManager", () => ({
	getUserInfo: vi.fn(),
}));

const fullAccess = {
	hasLogsAccess: true,
	hasObservabilityAccess: true,
	hasModelProvidersAccess: true,
	hasMCPGatewayAccess: true,
	hasPluginsAccess: true,
	hasUsersAccess: true,
	hasUserProvisioningAccess: true,
	hasAuditLogsAccess: true,
	hasCustomersAccess: true,
	hasTeamsAccess: true,
	hasRbacAccess: true,
	hasVirtualKeysAccess: true,
	hasGovernanceAccess: true,
	hasRoutingRulesAccess: true,
	hasGuardrailsProvidersAccess: true,
	hasGuardrailsConfigAccess: true,
	hasClusterConfigAccess: true,
	isAdaptiveRoutingAllowed: true,
	hasSettingsAccess: true,
	hasPromptRepositoryAccess: true,
	hasPromptDeploymentStrategyAccess: true,
	isDbConnected: true,
};

const collectWorkspaceUrls = (items: Array<{ url: string; subItems?: Array<{ url: string }> }>) => {
	const urls = new Set<string>();

	for (const item of items) {
		if (item.url.startsWith("/workspace")) {
			urls.add(item.url);
		}

		for (const subItem of item.subItems ?? []) {
			if (subItem.url.startsWith("/workspace")) {
				urls.add(subItem.url);
			}
		}
	}

	return Array.from(urls).sort();
};

describe("route titles", () => {
	let buildSidebarItems: ((options: typeof fullAccess) => Array<{ url: string; subItems?: Array<{ url: string }> }>) | undefined;

	beforeAll(async () => {
		const sidebarModule = await import("@/components/sidebar");
		buildSidebarItems = sidebarModule.buildSidebarItems;
	});

	it("resolves friendly titles for key workspace routes", async () => {
		const routeTitleModule = await import("./routeTitle");

		expect(routeTitleModule.resolveRouteTitle("/workspace/dashboard")).toBe("Dashboard");
		expect(routeTitleModule.resolveRouteTitle("/workspace/config/api-keys")).toBe("API Keys");
		expect(routeTitleModule.resolveRouteTitle("/workspace/governance/virtual-keys")).toBe("Virtual Keys");
		expect(routeTitleModule.resolveRouteTitle("/workspace/mcp-auth-config")).toBe("Auth Config");
		expect(routeTitleModule.resolveRouteTitle("/workspace/alert-channels")).toBe("Alert Channels");
	});

	it("builds browser titles with the custom site title", async () => {
		const routeTitleModule = await import("./routeTitle");

		expect(routeTitleModule.getDocumentTitle("/workspace/dashboard", "ZDFan AI Gateway")).toBe("Dashboard | ZDFan AI Gateway");
		expect(routeTitleModule.getDocumentTitle("/workspace", "ZDFan AI Gateway")).toBe("ZDFan AI Gateway");
		expect(routeTitleModule.getDocumentTitle("/workspace/alert-channels", "ZDFan AI Gateway")).toBe("Alert Channels | ZDFan AI Gateway");
	});

	it("updates the browser title only when it actually changed", async () => {
		const routeTitleModule = await import("./routeTitle");
		const fakeDocument = { title: "Bifrost Console" };

		expect(routeTitleModule.syncDocumentTitle("MCP Logs | Bifrost Console", fakeDocument)).toBe(true);
		expect(fakeDocument.title).toBe("MCP Logs | Bifrost Console");
		expect(routeTitleModule.syncDocumentTitle("MCP Logs | Bifrost Console", fakeDocument)).toBe(false);
	});

	it("covers every current sidebar workspace link with a friendly title", async () => {
		if (!buildSidebarItems) {
			throw new Error("buildSidebarItems was not loaded");
		}

		const routeTitleModule = await import("./routeTitle");
		const urls = collectWorkspaceUrls(buildSidebarItems(fullAccess));

		for (const url of urls) {
			expect(routeTitleModule.resolveRouteTitle(url)).toBeTruthy();
		}
	});
});
