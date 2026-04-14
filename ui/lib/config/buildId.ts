const BUILD_ID_ENV_KEYS = ["BIFROST_BUILD_ID", "GITHUB_SHA", "VERCEL_GIT_COMMIT_SHA", "SOURCE_VERSION"] as const;

type BuildIdEnv = Record<string, string | undefined>;

const sanitizeBuildId = (value?: string, maxLength?: number) => {
	const trimmedValue = value?.trim();
	if (!trimmedValue) {
		return undefined;
	}

	if (!maxLength) {
		return trimmedValue;
	}

	return trimmedValue.slice(0, maxLength);
};

export const resolveCustomBuildId = (env: BuildIdEnv) => {
	for (const key of BUILD_ID_ENV_KEYS) {
		const buildId = sanitizeBuildId(env[key], key === "BIFROST_BUILD_ID" ? undefined : 12);
		if (buildId) {
			return buildId;
		}
	}

	return undefined;
};
