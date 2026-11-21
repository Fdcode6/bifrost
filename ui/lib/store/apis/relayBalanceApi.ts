import type {
	RelayBalanceSettingsView,
	RelayBalancesResponse,
	TestRelayBalanceBarkResponse,
	UpdateRelayBalanceSettingsRequest,
} from "@/lib/types/relayBalance";

import { baseApi } from "./baseApi";

export const relayBalanceApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		getRelayBalances: builder.query<RelayBalancesResponse, void>({
			query: () => "/relay-balances",
			providesTags: ["RelayBalances"],
		}),
		refreshRelayBalances: builder.mutation<RelayBalancesResponse, { force?: boolean } | void>({
			query: (arg) => ({
				url: "/relay-balances/refresh",
				method: "POST",
				params: arg && arg.force ? { force: "true" } : undefined,
			}),
			invalidatesTags: ["RelayBalances"],
		}),
		getRelayBalanceSettings: builder.query<RelayBalanceSettingsView, void>({
			query: () => "/relay-balances/settings",
			providesTags: ["RelayBalances"],
		}),
		updateRelayBalanceSettings: builder.mutation<RelayBalanceSettingsView, UpdateRelayBalanceSettingsRequest>({
			query: (body) => ({
				url: "/relay-balances/settings",
				method: "PUT",
				body,
			}),
			invalidatesTags: ["RelayBalances"],
		}),
		testRelayBalanceBark: builder.mutation<TestRelayBalanceBarkResponse, void>({
			query: () => ({
				url: "/relay-balances/test-bark",
				method: "POST",
			}),
		}),
	}),
});

export const {
	useGetRelayBalancesQuery,
	useRefreshRelayBalancesMutation,
	useGetRelayBalanceSettingsQuery,
	useUpdateRelayBalanceSettingsMutation,
	useTestRelayBalanceBarkMutation,
} = relayBalanceApi;
