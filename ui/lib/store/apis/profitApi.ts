import type {
	ProfitBackfillResult,
	ProfitBreakdownResponse,
	ProfitDailyResponse,
	ProfitPreset,
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
		updateProfitSettings: builder.mutation<ProfitSettings, Pick<ProfitSettings, "sell_input_per_1m_usd" | "sell_output_per_1m_usd" | "timezone">>({
			query: (body) => ({
				url: "/profit/settings",
				method: "PUT",
				body,
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
	useGetProfitBreakdownQuery,
	useGetProfitDailyQuery,
	useGetProfitSettingsQuery,
	useGetProfitSummaryQuery,
	useUpdateProfitSettingsMutation,
} = profitApi;
