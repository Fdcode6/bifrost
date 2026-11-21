import type {
	RelayBalanceSettingsView,
	RelayBalancesResponse,
	RelayRechargeRecord,
	RelayRechargeSyncResponse,
	RelayRechargesResponse,
	TestRelayBalanceBarkResponse,
	UpdateRelayBalanceSettingsRequest,
	UpsertRelayRechargeRequest,
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
		getRelayRecharges: builder.query<
			RelayRechargesResponse,
			{ site_id?: string; start_date?: string; end_date?: string; limit?: number } | void
		>({
			query: (arg) => ({
				url: "/relay-recharges",
				params: arg
					? {
							...(arg.site_id && arg.site_id !== "all" ? { site_id: arg.site_id } : {}),
							...(arg.start_date ? { start_date: arg.start_date } : {}),
							...(arg.end_date ? { end_date: arg.end_date } : {}),
							...(arg.limit ? { limit: arg.limit } : {}),
						}
					: undefined,
			}),
			providesTags: ["RelayBalances"],
		}),
		syncRelayRecharges: builder.mutation<RelayRechargeSyncResponse, void>({
			query: () => ({
				url: "/relay-recharges/sync",
				method: "POST",
			}),
			invalidatesTags: ["RelayBalances"],
		}),
		createRelayRecharge: builder.mutation<RelayRechargeRecord, UpsertRelayRechargeRequest>({
			query: (body) => ({
				url: "/relay-recharges",
				method: "POST",
				body,
			}),
			invalidatesTags: ["RelayBalances"],
		}),
		updateRelayRecharge: builder.mutation<RelayRechargeRecord, { id: string } & Partial<UpsertRelayRechargeRequest>>({
			query: ({ id, ...body }) => ({
				url: `/relay-recharges/${encodeURIComponent(id)}`,
				method: "PUT",
				body,
			}),
			invalidatesTags: ["RelayBalances"],
		}),
		deleteRelayRecharge: builder.mutation<{ success: boolean; id: string }, string>({
			query: (id) => ({
				url: `/relay-recharges/${encodeURIComponent(id)}`,
				method: "DELETE",
			}),
			invalidatesTags: ["RelayBalances"],
		}),
	}),
});

export const {
	useGetRelayBalancesQuery,
	useRefreshRelayBalancesMutation,
	useGetRelayBalanceSettingsQuery,
	useUpdateRelayBalanceSettingsMutation,
	useTestRelayBalanceBarkMutation,
	useGetRelayRechargesQuery,
	useSyncRelayRechargesMutation,
	useCreateRelayRechargeMutation,
	useUpdateRelayRechargeMutation,
	useDeleteRelayRechargeMutation,
} = relayBalanceApi;
