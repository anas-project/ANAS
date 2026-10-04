package runner

// fakeComputeEnsureReport is the single stdout line a compute Provider's
// ensure prints: readiness plus the lease bridge Core records (INCUS-R-159).
const fakeComputeEnsureReport = `{"exists":true,"ready":true,"restricted":true,"quota_enforced":true,` +
	`"network":{"bridge":"lease120067207a","ipv4_subnet":"10.101.0.0/24","ipv4_gateway":"10.101.0.1"}}`

const fakeComputeEnsureEcho = "printf '%s\\n' '" + fakeComputeEnsureReport + "'\n"
