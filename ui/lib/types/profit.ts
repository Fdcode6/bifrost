export type ProfitPreset = "today" | "yesterday" | "7d" | "all";

export interface ProfitSettings {
	id?: string;
	sell_input_per_1m_usd: number;
	sell_output_per_1m_usd: number;
	timezone: string;
	created_at?: string;
	updated_at?: string;
}

export interface ProfitQuery {
	start_day?: string;
	end_day?: string;
}

export interface ProfitSummary {
	revenue_usd: number;
	cost_usd: number;
	profit_usd: number;
	gross_margin?: number | null;
	request_count: number;
	success_count: number;
	error_count: number;
	prompt_tokens: number;
	completion_tokens: number;
	total_tokens: number;
	missing_cost_count: number;
	missing_tokens_count: number;
}

export interface ProfitDailyBucket extends ProfitSummary {
	business_day: string;
}

export interface ProfitBreakdownRow extends ProfitSummary {
	provider: string;
	model: string;
}

export interface ProfitSummaryResponse {
	preset: ProfitPreset;
	query: ProfitQuery;
	data: ProfitSummary;
}

export interface ProfitDailyResponse {
	query: ProfitQuery;
	days: ProfitDailyBucket[];
}

export interface ProfitBreakdownResponse {
	preset: ProfitPreset;
	query: ProfitQuery;
	rows: ProfitBreakdownRow[];
}

export interface ProfitBackfillResult {
	processed: number;
	created: number;
	updated: number;
	skipped: number;
}

export interface ProfitReconciliationStatus {
	missing_event_count: number;
	last_run_at?: string | null;
	next_run_at?: string | null;
	interval_seconds: number;
	batch_limit: number;
	last_result?: ProfitBackfillResult | null;
	last_error?: string;
}
