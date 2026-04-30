// Package secrets is the umbrella for cronfoundry's secret subsystem.
//
// Two halves, separated by a trust boundary:
//
//   - secrets/runner is loaded by the runner subprocess. It reads
//     CRONFOUNDRY_SECRET_<NAME> environment variables that the scheduler
//     exported for one specific run. The runner has no access to the
//     persistent store and never sees the master key.
//
//   - secrets/server is loaded by the cronfoundry server binary. It is
//     the only component that holds the master key (CRONFOUNDRY_MASTER_KEY,
//     base64-encoded 32 bytes) and that talks to persistent storage.
//     SecretStore is the single external contract; implementations:
//       - secrets/server.EnvelopePostgresStore — self-hosted Postgres
//         with envelope encryption (DEK wrapped under the master key).
//       - secrets/server/azurekv.KeyVaultStore — Azure Key Vault.
//     The backend is selected at startup from CRONFOUNDRY_SECRETS_BACKEND.
//
// Per-run scoped manifest (PRD FR-6.4): when the server prepares a run,
// it resolves only the secrets named in the skill manifest and exports
// them as CRONFOUNDRY_SECRET_<NAME> env vars to the runner. The runner
// has no direct access to the store. This contract is audit-logged
// today; cryptographic enforcement (KV-proxy sidecar) is deferred.
package secrets
