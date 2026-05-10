import type {
	ProfitBackfillResult,
	ProfitBreakdownResponse,
	ProfitDailyResponse,
	ProfitPreset,
	ProfitReconciliationStatus,
	RoutingRuleProfitSettingsResponse,
	ProfitSettings,
	ProfitSummaryResponse,
} from "@/lib/types/profit";

import { baseApi } from "./baseApi";

export const profitApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getProfitSettings: builder.query<ProfitSettings, void>({
			query: () => "/profit/settings",
			providesTags: ["Profit"],
		}),
		updateProfitSettings: builder.mutation<
			ProfitSettings,
			Pick<ProfitSettings, "sell_input_per_1m_usd" | "sell_output_per_1m_usd" | "timezone">
		>({
			query: (body) => ({
				url: "/profit/settings",
				method: "PUT",
				body,
			}),
			invalidatesTags: ["Profit"],
		}),
		getRoutingRuleProfitSettings: builder.query<RoutingRuleProfitSettingsResponse, void>({
			query: () => "/profit/routing-rule-prices",
			providesTags: ["Profit"],
		}),
		updateRoutingRuleProfitSettings: builder.mutation<
			ProfitSettings,
			{ routingRuleId: string } & Pick<ProfitSettings, "sell_input_per_1m_usd" | "sell_output_per_1m_usd" | "timezone">
		>({
			query: ({ routingRuleId, ...body }) => ({
				url: `/profit/routing-rule-prices/${encodeURIComponent(routingRuleId)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["Profit"],
		}),
		deleteRoutingRuleProfitSettings: builder.mutation<{ message: string }, string>({
			query: (routingRuleId) => ({
				url: `/profit/routing-rule-prices/${encodeURIComponent(routingRuleId)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["Profit"],
		}),
		getProfitSummary: builder.query<ProfitSummaryResponse, ProfitPreset>({
			query: (preset) => ({
				url: "/profit/summary",
				params: { preset },
			}),
			providesTags: ["Profit"],
		}),
		getProfitDaily: builder.query<ProfitDailyResponse, number>({
			query: (days) => ({
				url: "/profit/daily",
				params: { days },
			}),
			providesTags: ["Profit"],
		}),
		getProfitBreakdown: builder.query<ProfitBreakdownResponse, ProfitPreset>({
			query: (preset) => ({
				url: "/profit/breakdown",
				params: { preset },
			}),
			providesTags: ["Profit"],
		}),
		getProfitReconciliationStatus: builder.query<ProfitReconciliationStatus, void>({
			query: () => "/profit/reconciliation-status",
			providesTags: ["Profit"],
		}),
		backfillProfitEvents: builder.mutation<ProfitBackfillResult, { limit?: number }>({
			query: (body) => ({
				url: "/profit/backfill",
				method: "POST",
				body,
			}),
			invalidatesTags: ["Profit"],
		}),
	}),
});

export const {
	useBackfillProfitEventsMutation,
	useDeleteRoutingRuleProfitSettingsMutation,
	useGetProfitBreakdownQuery,
	useGetProfitDailyQuery,
	useGetProfitReconciliationStatusQuery,
	useGetRoutingRuleProfitSettingsQuery,
	useGetProfitSettingsQuery,
	useGetProfitSummaryQuery,
	useUpdateRoutingRuleProfitSettingsMutation,
	useUpdateProfitSettingsMutation,
} = profitApi;
