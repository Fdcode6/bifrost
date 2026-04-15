import type { Page } from "@playwright/test";

import { expect, test } from "../../core/fixtures/base.fixture";

async function loginToBifrostIfNeeded(page: Page) {
	const authResponse = await page.context().request.get("/api/session/is-auth-enabled");
	expect(authResponse.ok()).toBeTruthy();

	const authState = (await authResponse.json()) as {
		is_auth_enabled?: boolean;
		has_valid_token?: boolean;
	};

	if (!authState.is_auth_enabled || authState.has_valid_token) {
		return;
	}

	const username = process.env.BIFROST_ADMIN_USERNAME || process.env.ADMIN_USERNAME;
	const password = process.env.BIFROST_ADMIN_PASSWORD || process.env.ADMIN_PASSWORD;

	if (!username || !password) {
		throw new Error("Authentication is enabled but admin credentials are missing.");
	}

	const loginResponse = await page.context().request.post("/api/session/login", {
		data: { username, password },
	});
	expect(loginResponse.ok()).toBeTruthy();
}

test.describe("LLM Logs Request Grouping Smoke", () => {
	test("should show grouped request summary surfaces", async ({ logsPage, page }) => {
		await loginToBifrostIfNeeded(page);
		await logsPage.goto();

		await expect(page.getByText("Request Success Rate")).toBeVisible();
		await expect(page.getByText("Attempt Success Rate")).toBeVisible();
		await expect(page.getByText("Avg Final Latency")).toBeVisible();
		await expect(page.getByTestId("logs-final-success-distribution-card")).toBeVisible();
	});

	test("should expand a grouped request row when one is available", async ({ logsPage, page }) => {
		await loginToBifrostIfNeeded(page);
		await logsPage.goto();

		const groupRow = page.locator('[data-testid^="logs-request-group-"]').first();
		test.skip((await groupRow.count()) === 0, "No multi-attempt request group is available in this environment");

		await groupRow.locator('button[aria-label*="request group"]').first().click();
		await expect(page.locator('[data-testid^="logs-request-attempt-"]').first()).toBeVisible();
	});
});
