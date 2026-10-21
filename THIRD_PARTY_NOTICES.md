# Third-party notices

The root [MIT License](LICENSE) covers project-authored code and documentation. Third-party packages retain their original licenses and copyright notices. It does not grant rights to third-party datasets, platform content, credentials, or user-supplied investigation material.

The project references dependencies through `go.mod`, `go.sum`, `frontend/package.json`, and `frontend/package-lock.json`. Preserve their applicable notices when redistributing dependencies or compiled artifacts.

## Principal dependencies

| Dependency | License reference |
| --- | --- |
| Gin | [MIT](https://github.com/gin-gonic/gin/blob/v1.12.0/LICENSE) |
| zerolog | [MIT](https://github.com/rs/zerolog/blob/v1.35.1/LICENSE) |
| Excelize | [BSD-3-Clause](https://github.com/qax-os/excelize/blob/v2.10.1/LICENSE) |
| modernc.org/sqlite | [Upstream license](https://gitlab.com/cznic/sqlite/-/blob/v1.52.0/LICENSE) |
| React | [Upstream license](https://github.com/facebook/react/blob/main/LICENSE) |
| Ant Design | [Upstream license](https://github.com/ant-design/ant-design/blob/master/LICENSE) |
| React Flow | [Upstream license](https://github.com/xyflow/xyflow/blob/main/LICENSE) |
| Vite | [Upstream license](https://github.com/vitejs/vite/blob/main/LICENSE) |

The frontend lockfile also declares licenses including ISC, Apache-2.0, BSD-3-Clause, MPL-2.0, 0BSD, and dual-license expressions. This table names principal components; the package versions and upstream license files determine the complete dependency notices for a particular distribution.

ClickHouse, DuckDB CLI, database servers, and the separately deployed SQD Cloud Worker have their own distribution and license terms. They are not relicensed by this repository.
