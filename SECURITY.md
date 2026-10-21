# Security policy

## Deployment model

Funds ETL is a local investigation and data-processing application. Its current backend binds to all network interfaces on the configured port and does not provide a general application login or tenant-isolation layer. CORS configuration is not access control.

Limit access to the machine and port with host firewall rules. Remote use requires an authenticated gateway, TLS, and access restrictions covering the entire application. The loopback checks on diagnostic endpoints do not protect other APIs; a local reverse proxy can also appear as a loopback client.

Database connections, SQL tools, uploads, exports, and investigation functions operate with the permissions of the configured accounts and server process. Use dedicated database accounts with the permissions needed for the task.

## Reporting a vulnerability

Use the repository's **Security → Report a vulnerability** option when available. If private reporting is unavailable, open an issue requesting a private contact channel, without including exploit steps, credentials, personal data, or case material. The maintainer will arrange a private exchange.

For a private report, include the affected commit, environment, observed behavior, expected behavior, and a minimal reproduction using synthetic data. Allow time for triage and a fix before publishing a working exploit.

There is no published security-response service-level agreement or maintained release support matrix. Report findings against the current `main` branch and identify any older affected revision.

## Credentials and investigation data

Keep provider keys, browser sessions, database passwords, uploads, raw evidence, and exports outside version control. Use environment variables or local credential files. `.gitignore` does not remove files that are already tracked or erase older commits.

Invalidate credentials exposed in Git history at the issuing service. Deleting the current file does not revoke a token. History cleanup requires a separate coordinated operation because it changes commit identities and affects existing clones.

Use synthetic or explicitly publishable fixtures for issues, tests, examples, and screenshots. Check terminal output, conversation attachments, generated source snapshots, and database dumps before publishing.

## Audit status

The 2026-10-21 review found unresolved dependency advisories and historical authentication material. Raw audit artifacts and credential locations are kept locally rather than attached to public issues. This documentation and license update does not close those findings.

Dependency inventory checks can be repeated with:

```powershell
govulncheck ./...
cd frontend
npm audit --package-lock-only --registry=https://registry.npmjs.org
```

Use a 64-bit `govulncheck` for the full call graph. An audit result depends on the Go toolchain, target platform, lockfile, advisory database, and scan date.
