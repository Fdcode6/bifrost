import { describe, expect, it } from "vitest";
import { availableProviderConfigTabs } from "./providerConfigTabs";

describe("availableProviderConfigTabs", () => {
	it("shows pricing overrides for provider configuration", () => {
		const tabs = availableProviderConfigTabs({
			hasCustomProviderConfig: true,
			hasGovernanceAccess: true,
			isAnthropicFamily: false,
			isOpenAI: false,
		});

		expect(tabs.map((tab) => tab.id)).toContain("pricing-overrides");
	});
});
