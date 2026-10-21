# Contributing

Funds ETL is maintained by LINDUJIAN (GitHub: [Euxripides](https://github.com/Euxripides)). Contributions to parsers, field mappings, data quality, downloads, tests, documentation, and usability are welcome.

## Issues and proposals

For a bug, describe the affected commit, operating system, Go and Node.js versions, reproduction steps, and expected result. Use a small synthetic example for import or mapping problems. For a larger change, explain the intended behavior and affected modules before implementing it.

Report security problems through [SECURITY.md](SECURITY.md). Do not publish credentials, browser cookies, real financial records, or investigation material in an issue or pull request.

## Development

The current scripts target Windows and PowerShell. See the [README](README.md) for setup, external services, fixed data paths, and the separate SQD Cloud Worker.

```powershell
go test ./internal/... -short -count=1 -timeout=3m
go vet ./...
go build -o bin\etl-server.exe .\cmd\server\

cd frontend
npm ci
npm run build
```

The full Go suite includes longer scenarios and tests that depend on external services. Identify the command and environment when reporting a pass, failure, or skipped test.

## Pull requests

- Keep each change focused and preserve unrelated work.
- Preserve API paths, transaction-field semantics, and existing import compatibility unless the change explicitly requires migration.
- Add a regression test for a behavioral fix and describe how it was verified.
- Document new configuration, dependencies, and storage changes.
- Update `docs/AI_HANDOFF.md` and `docs/CHANGELOG_AI.md` as required by the project instructions.
- Do not use production download batches or real case data for an automated test.

The local pre-commit hook, when installed, regenerates `docs/cpg/` for Go source changes. Review generated changes before including them.

## License

The project-authored code is distributed under the [MIT License](LICENSE), copyright 2026 LINDUJIAN. Submit only code and examples you are entitled to contribute. Preserve existing third-party notices; dependency licenses are not replaced by the project's MIT license.
