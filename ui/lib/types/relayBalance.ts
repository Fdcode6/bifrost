export type RelayBalanceSiteKind = "polo_usage_token" | "bai_v1_balance" | "new_api_user_self";

export type RelayBalanceSiteStatus = "ok" | "low" | "urgent" | "unconfigured" | "error" | "pending";

export interface RelayBalanceSiteSnapshot {
	id: string;
	name: string;
	kind: RelayBalanceSiteKind;
	base_url: string;
	recharge_url?: string;
	provider_prefix: string;
	cny_per_site_unit: number;
	threshold_cny: number;
	urgent_threshold_cny: number;
	enabled: boolean;
	status: RelayBalanceSiteStatus;
	site_balance_usd?: number | null;
	balance_cny?: number | null;
	site_used_usd?: number | null;
	spend_24h_cny: number;
	estimated_days_left?: number | null;
	key_source?: string;
	last_checked_at?: string | null;
	last_success_at?: string | null;
	last_error?: string;
	consecutive_errors: number;
	alert_active: boolean;
	last_alert_tier?: string;
	last_alert_at?: string | null;
	pending_after_quiet: boolean;
	needs_access_token: boolean;
	has_access_token: boolean;
	user_id?: string;
}

export interface RelayBalancesResponse {
	enabled: boolean;
	bark_enabled: boolean;
	bark_configured: boolean;
	poll_interval_seconds: number;
	total_balance_cny: number;
	low_count: number;
	urgent_count: number;
	error_count: number;
	last_poll_at?: string | null;
	next_poll_at?: string | null;
	sites: RelayBalanceSiteSnapshot[];
}

export interface RelayBalanceSiteSettingsView {
	id: string;
	name: string;
	kind: RelayBalanceSiteKind;
	base_url: string;
	recharge_url?: string;
	provider_prefix: string;
	quota_per_unit: number;
	cny_per_site_unit: number;
	threshold_cny: number;
	urgent_threshold_cny: number;
	enabled: boolean;
	user_id?: string;
	has_access_token: boolean;
	access_token_source?: string;
	access_token_masked?: string;
}

export interface RelayBalanceSettingsView {
	enabled: boolean;
	poll_interval_seconds: number;
	timezone: string;
	bark_enabled: boolean;
	bark_group: string;
	bark_configured: boolean;
	bark_source?: string;
	bark_url_masked?: string;
	cooldown_hours: number;
	urgent_cooldown_hours: number;
	quiet_hours_enabled: boolean;
	quiet_start_hour: number;
	quiet_end_hour: number;
	sites: RelayBalanceSiteSettingsView[];
}

export interface UpdateRelayBalanceSiteInput {
	id: string;
	name: string;
	kind: RelayBalanceSiteKind;
	base_url: string;
	recharge_url?: string;
	provider_prefix: string;
	quota_per_unit: number;
	cny_per_site_unit: number;
	threshold_cny: number;
	urgent_threshold_cny: number;
	enabled: boolean;
	user_id?: string;
	access_token?: string;
	clear_access_token?: boolean;
}

export interface UpdateRelayBalanceSettingsRequest {
	enabled: boolean;
	poll_interval_seconds: number;
	timezone: string;
	bark_enabled: boolean;
	bark_group: string;
	bark_url?: string;
	clear_bark_url?: boolean;
	cooldown_hours: number;
	urgent_cooldown_hours: number;
	quiet_hours_enabled: boolean;
	quiet_start_hour: number;
	quiet_end_hour: number;
	sites: UpdateRelayBalanceSiteInput[];
}

export interface TestRelayBalanceBarkResponse {
	success: boolean;
	message: string;
	title: string;
	body: string;
}

export type RelayRechargeSource = "api_sync" | "balance_delta" | "manual";
export type RelayRechargeStatus = "success" | "pending_confirm";
export type RelayRechargeSyncMode = "api_topup" | "api_log" | "balance_delta" | "unconfigured";

export interface RelayRechargeRecord {
	id: string;
	site_id: string;
	site_name: string;
	source: RelayRechargeSource;
	external_id?: string;
	recharged_at: string;
	site_amount_usd: number;
	paid_amount: number;
	paid_currency: string;
	paid_cny: number;
	payment_method?: string;
	status: RelayRechargeStatus;
	note?: string;
	created_at: string;
	updated_at: string;
}

export interface RelayRechargeSiteSummary {
	site_id: string;
	site_name: string;
	record_count: number;
	pending_count: number;
	total_site_usd: number;
	total_paid_cny: number;
	last_recharged_at?: string | null;
}

export interface RelayRechargeSummary {
	total_count: number;
	pending_count: number;
	total_site_usd: number;
	total_paid_cny: number;
	by_site: RelayRechargeSiteSummary[];
}

export interface RelayRechargeSiteMeta {
	id: string;
	name: string;
	kind: RelayBalanceSiteKind;
	recharge_url?: string;
	cny_per_site_unit: number;
	sync_mode: RelayRechargeSyncMode;
}

export interface RelayRechargesResponse {
	last_sync_at?: string | null;
	summary: RelayRechargeSummary;
	sites: RelayRechargeSiteMeta[];
	items: RelayRechargeRecord[];
}

export interface RelayRechargeSiteSyncResult {
	site_id: string;
	site_name: string;
	sync_mode: RelayRechargeSyncMode;
	fetched: number;
	inserted: number;
	error?: string;
}

export interface RelayRechargeSyncResponse {
	synced_at: string;
	total_inserted: number;
	results: RelayRechargeSiteSyncResult[];
	data: RelayRechargesResponse;
}

export interface UpsertRelayRechargeRequest {
	site_id: string;
	recharged_at?: string;
	site_amount_usd: number;
	paid_amount?: number;
	paid_currency?: string;
	paid_cny: number;
	payment_method?: string;
	status?: RelayRechargeStatus;
	external_id?: string;
	note?: string;
}
