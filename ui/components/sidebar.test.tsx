import { beforeAll, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/store", () => ({
	useGetCoreConfigQuery: vi.fn(),
	useGetLatestReleaseQuery: vi.fn(),
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

let sidebarModule: any;

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

const flattenTitles = (items: Array<{ title: string; subItems?: Array<{ title: string }> }>) => {
	return items.flatMap((item) => [item.title, ...(item.subItems?.map((subItem) => subItem.title) ?? [])]);
};

describe("sidebar helpers", () => {
	beforeAll(async () => {
		sidebarModule = await import("./sidebar");
	});

	it("does not expose an Evals navigation item", () => {
		const buildSidebarItems = sidebarModule.buildSidebarItems;

		expect(buildSidebarItems).toBeTypeOf("function");

		const items = buildSidebarItems(fullAccess);

		expect(flattenTitles(items)).not.toContain("Evals");
	});

	it("does not create a new release promo card", () => {
		const buildPromoCards = sidebarModule.buildPromoCards;

		expect(buildPromoCards).toBeTypeOf("function");

		const cards = buildPromoCards({
			restartRequiredReason: "Restart needed",
			mounted: true,
			isProductionSetupDismissed: false,
		});

		expect(cards.map((card: { id: string }) => card.id)).not.toContain("new-release");
	});
});
