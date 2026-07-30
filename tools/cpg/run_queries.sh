#!/usr/bin/env bash
# Run all CodeQL CPG queries and export results
# Usage: bash run_queries.sh <project-root>

set -e
ROOT="${1:-.}"
DB="E:/codex/etl/docs/codeql-db"
QUERIES="E:/codex/etl/tools/cpg/queries"
OUT="E:/codex/etl/docs/cpg/codeql"

export PATH="/d/app/codeql/codeql:$PATH"

mkdir -p "$OUT"

echo "==> Running CodeQL CPG Queries..."
echo "    DB: $DB"
echo ""

run_query() {
    local name="$1"
    local ql="$QUERIES/$name.ql"
    local bqrs="$OUT/$name.bqrs"
    local csv="$OUT/$name.csv"
    echo "    $name..."
    codeql query run --database="$DB" --output="$bqrs" -- "$ql" 2>&1
    codeql bqrs decode --format=csv --output="$csv" "$bqrs" 2>&1
    local lines=$(wc -l < "$csv" 2>/dev/null || echo 0)
    echo "      -> $csv ($lines rows)"
}

run_query "package_deps"
run_query "functions"
run_query "types"
run_query "cfg"
run_query "call_graph"

echo ""
echo "==> Done. Results in $OUT"
echo ""
ls -lh "$OUT/" 2>/dev/null

# Export SARIF
echo ""
echo "==> Exporting SARIF..."
codeql database analyze "$DB" --format=sarif-latest --output="$OUT/cpg.sarif" "$QUERIES" 2>&1
echo "    -> $OUT/cpg.sarif"
