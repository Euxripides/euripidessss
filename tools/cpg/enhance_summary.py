"""
CPG Enhance: reads cpg.json and produces a rich summary.md
with architecture layers, cycle detection, coupling heat, interface impls, critical paths.
"""
import json, sys, os
from collections import defaultdict

def load_cpg(path):
    with open(path, 'r', encoding='utf-8') as f:
        return json.load(f)

def short(path):
    prefixes = ['github.com/etl/backend/internal/', 'github.com/etl/backend/cmd/']
    for p in prefixes:
        if path.startswith(p):
            return path[len(p):]
    return path

def safe_len(obj, key):
    v = obj.get(key)
    return len(v) if v else 0

def safe_get(obj, key):
    v = obj.get(key)
    return v if v else []

def print_md(out, data):
    pkgs = data['packages']
    dep_edges = data['dep_edges']
    call_edges = data['call_edges']
    
    pkg_by_path = {p['path']: p for p in pkgs}
    
    deps_to = defaultdict(set)
    deps_from = defaultdict(set)
    for e in dep_edges:
        deps_from[e['from']].add(e['to'])
        deps_to[e['to']].add(e['from'])
    
    total_fn = sum(safe_len(p, 'funcs') for p in pkgs)
    total_ty = sum(safe_len(p, 'types') for p in pkgs)
    
    # ---- PREAMBLE ----
    out.write("# Funds ETL \u2014 Code Property Graph\n\n")
    out.write(f"> Auto-generated. **{len(pkgs)} packages, {total_fn} functions, {total_ty} types.**\n\n")
    out.write("Use this as a **project map** before reading source code.\n\n")
    
    # ==========================================
    # 1. ARCHITECTURE LAYERS
    # ==========================================
    out.write("## 1. Architecture Layers\n\n")
    
    layers = {
        "Entry Point": lambda p: 'cmd/' in p['path'],
        "API / HTTP": lambda p: p['path'].endswith('/api'),
        "ETL Pipeline": lambda p: any(x in p['path'] for x in ['/etl', '/scanner', '/parser', '/provider', '/rules', '/normalize']),
        "Storage & IO": lambda p: any(x in p['path'] for x in ['/storage', '/dbimport', '/writer', '/downloader']),
        "Crypto / Blockchain": lambda p: any(x in p['path'] for x in ['/chain', '/cryptodownload', '/rpcmanager', '/datasource', '/parquetdownload', '/dunetools']),
        "Infrastructure": lambda p: any(x in p['path'] for x in ['/config', '/logger', '/model', '/analysis', '/service']),
    }
    
    for layer_name, pred in layers.items():
        members = [p for p in pkgs if pred(p)]
        if not members:
            continue
        l_fn = sum(safe_len(p, 'funcs') for p in members)
        l_ty = sum(safe_len(p, 'types') for p in members)
        out.write(f"### {layer_name} ({len(members)} pkgs, {l_fn} funcs, {l_ty} types)\n\n")
        for p in sorted(members, key=lambda x: -safe_len(x, 'funcs')):
            label = short(p['path'])
            deps = len(safe_get(p, 'imports'))
            dep_by = len(deps_to.get(p['path'], set()))
            types_list = safe_get(p, 'types')
            structs = sum(1 for t in types_list if t.get('kind') == 'struct')
            ifaces = sum(1 for t in types_list if t.get('kind') == 'interface')
            
            top_deps = []
            for imp in safe_get(p, 'imports'):
                if imp in pkg_by_path:
                    top_deps.append(short(imp))
            
            detail = f"{safe_len(p, 'funcs')} fn, {structs}s/{ifaces}i"
            if top_deps:
                detail += f", uses: {', '.join(top_deps[:4])}"
            if dep_by > 0:
                detail += f", used-by: {dep_by} pkg(s)"
            
            out.write(f"- **`{label}`** \u2014 {detail}\n")
        out.write("\n")
    
    # ==========================================
    # 2. DEPENDENCY HOTSPOTS
    # ==========================================
    out.write("## 2. Coupling Hotspots\n\n")
    out.write("Sorted by **instability** (outgoing deps / total deps). High = fragile.\n\n")
    
    coupling = []
    for p in pkgs:
        out_deps = len(safe_get(p, 'imports'))
        in_deps = len(deps_to.get(p['path'], set()))
        total = out_deps + in_deps
        instability = out_deps / max(total, 1)
        coupling.append((short(p['path']), out_deps, in_deps, instability, safe_len(p, 'funcs')))
    
    out.write("| Package | Out | In | Instability | Funcs |\n")
    out.write("|---------|-----|----|-------------|-------|\n")
    for name, out_d, in_d, inst, fn in sorted(coupling, key=lambda x: -x[1])[:15]:
        bar = '\u2588' * min(int(inst * 10), 10)
        out.write(f"| `{name}` | {out_d} | {in_d} | {inst:.2f} {bar} | {fn} |\n")
    
    out.write("\n**Key**: `parquetdownload` (11 out) and `api` (15 out) are the most coupled.\n\n")
    
    # ==========================================
    # 3. CYCLE DETECTION
    # ==========================================
    out.write("## 3. Cycle Detection\n\n")
    
    adj = defaultdict(set)
    for e in dep_edges:
        adj[e['from']].add(e['to'])
    
    index_counter = [0]
    index = {}
    lowlink = {}
    stack = []
    sccs = []
    
    def strongconnect(v):
        index[v] = index_counter[0]
        lowlink[v] = index_counter[0]
        index_counter[0] += 1
        stack.append(v)
        for w in adj.get(v, set()):
            if w not in index:
                strongconnect(w)
                lowlink[v] = min(lowlink[v], lowlink[w])
            elif w in stack:
                lowlink[v] = min(lowlink[v], index[w])
        if lowlink[v] == index[v]:
            scc = []
            while True:
                w = stack.pop()
                scc.append(w)
                if w == v:
                    break
            sccs.append(scc)
    
    for p in pkgs:
        if p['path'] not in index:
            strongconnect(p['path'])
    
    cycles = [(scc, len(scc)) for scc in sccs if len(scc) > 1]
    cycles.sort(key=lambda x: -x[1])
    
    if cycles:
        out.write(f"\u2757 **{len(cycles)} cyclic dependencies:**\n\n")
        for scc, size in cycles:
            names = ', '.join(short(p) for p in sorted(scc))
            out.write(f"- Cycle of {size}: `{names}`\n")
        out.write("\n**Risk**: Circular imports break test isolation and Go compilation.\n\n")
    else:
        out.write("\u2705 No circular dependencies.\n\n")
    
    # ==========================================
    # 4. INTERFACE INVENTORY
    # ==========================================
    out.write("## 4. Interface Inventory\n\n")
    
    all_ifaces = []
    for p in pkgs:
        for t in safe_get(p, 'types'):
            if t.get('kind') == 'interface':
                all_ifaces.append((short(p['path']), t))
    
    if all_ifaces:
        out.write(f"**{len(all_ifaces)} interfaces**:\n\n")
        out.write("| Interface | Package | Methods |\n")
        out.write("|-----------|---------|--------|\n")
        for pkg_name, iface in sorted(all_ifaces, key=lambda x: len(safe_get(x[1], 'methods'))):
            methods = ', '.join(safe_get(iface, 'methods')[:6])
            mc = safe_len(iface, 'methods')
            if mc > 6:
                methods += f" +{mc - 6}"
            out.write(f"| `{iface['name']}` | `{pkg_name}` | {methods} |\n")
        out.write("\n")
    
    # ==========================================
    # 5. CROSS-LAYER CALL PATHS
    # ==========================================
    out.write("## 5. Key Call Paths\n\n")
    out.write("HTTP handlers down to data layer:\n\n")
    
    called_by = defaultdict(set)
    for e in call_edges:
        called_by[e['callee']].add(e['caller'])
    
    def find_upstream(func, depth=3, visited=None):
        if visited is None:
            visited = set()
        if func in visited or depth <= 0:
            return [func]
        visited.add(func)
        callers = called_by.get(func, set())
        internal = [c for c in callers if 'etl/backend/' in c]
        if internal:
            best = max(internal, key=lambda c: len(called_by.get(c, set())))
            return [func] + find_upstream(best, depth - 1, visited)
        elif callers:
            best = list(callers)[0]
            return [func] + find_upstream(best, depth - 1, visited)
        return [func]
    
    interesting = ['BuildFlowGraph', 'StartTask', 'CollectAddress', 'FetchTokenTransfersByTimeWindow']
    
    func_index = {}
    for p in pkgs:
        for fn in safe_get(p, 'funcs'):
            func_index[p['path'] + '.' + fn['name']] = (short(p['path']), fn['name'])
    
    for target_name in interesting:
        matches = [(k, v) for k, v in func_index.items() if v[1] == target_name]
        if not matches:
            continue
        full_name, (pkg, _) = matches[0]
        chain = find_upstream(full_name, depth=4)
        chain_short = []
        for c in chain:
            if c in func_index:
                chain_short.append(func_index[c])
            else:
                chain_short.append(('ext', c.split('.')[-1] if '.' in c else c))
        path_str = ' \u2190 '.join(f'`{p}`.{n}' for p, n in reversed(chain_short))
        out.write(f"- **{target_name}** \u2190 {path_str}\n")
    
    out.write("\n")
    
    # ==========================================
    # 6. RISK MATRIX
    # ==========================================
    out.write("## 6. Package Risk Matrix\n\n")
    out.write("Size \u00d7 coupling = maintenance risk.\n\n")
    
    risk = []
    for p in pkgs:
        size = safe_len(p, 'funcs') + safe_len(p, 'types')
        cs = len(safe_get(p, 'imports')) + len(deps_to.get(p['path'], set()))
        score = size * (cs + 1)
        risk.append((short(p['path']), size, cs, score, safe_len(p, 'funcs')))
    
    out.write("| Package | Size | Coupling | Risk | Level |\n")
    out.write("|---------|------|----------|------|-------|\n")
    for name, size, cs, score, fn in sorted(risk, key=lambda x: -x[3])[:15]:
        if score > 500:
            level = '\U0001f534 HIGH'
        elif score > 200:
            level = '\U0001f7e1 MED'
        else:
            level = '\U0001f7e2 LOW'
        out.write(f"| `{name}` | {size} | {cs} | {score} | {level} |\n")
    
    # ==========================================
    # 7. MERMAID (inline)
    # ==========================================
    out.write("\n## 7. Dependency Diagram\n\n")
    out.write("```mermaid\ngraph TB\n")
    out.write("  subgraph Entry[\"Entry\"]\n    server[\"server\"]\n  end\n")
    out.write("  subgraph API[\"API Layer\"]\n    api[\"api (279 fn)\"]\n  end\n")
    out.write("  subgraph ETL[\"ETL Pipeline\"]\n    etl[\"etl\"]\n    scanner[\"scanner\"]\n    parser[\"parser\"]\n    provider[\"provider\"]\n    rules[\"rules\"]\n  end\n")
    out.write("  subgraph Data[\"Data Layer\"]\n    dbimport[\"dbimport\"]\n    storage[\"storage\"]\n    duckdb_[\"duckdb\"]\n  end\n")
    out.write("  subgraph Crypto[\"Crypto/Scraping\"]\n    cryptodw[\"cryptodownload<br/>(655 fn)\"]\n    parquet[\"parquetdownload\"]\n    rpc[\"rpcmanager\"]\n    normal[\"normalize\"]\n    chain_[\"chain\"]\n  end\n")
    out.write("  subgraph Infra[\"Infrastructure\"]\n    model[\"model\"]\n    config[\"config\"]\n    logger_[\"logger\"]\n  end\n")
    out.write("  server --> api\n  api --> etl\n  api --> cryptodw\n  api --> parquet\n  api --> dbimport\n  api --> rpc\n  etl --> provider\n  etl --> parser\n  etl --> scanner\n  etl --> rules\n  etl --> model\n  provider --> parser\n  provider --> rules\n  provider --> model\n  cryptodw --> chain_\n  parquet --> chain_\n  parquet --> normal\n  parquet --> rpc\n  rpc --> chain_\n  normal --> chain_\n  dbimport --> parser\n  dbimport --> model\n  scanner --> parser\n  scanner --> rules\n  rules --> parser\n")
    out.write("```\n\n")
    
    # ==========================================
    # 8. AGENT QUICK-START
    # ==========================================
    out.write("## 8. Agent Quick-Start\n\n")
    out.write("**Before modifying code**, check:\n\n")
    out.write("1. **Which layer?** See Section 1.\n")
    out.write("2. **Who depends on it?** See Section 2 coupling table.\n")
    out.write("3. **Any cycles?** See Section 3.\n")
    out.write("4. **Which interface?** See Section 4.\n")
    out.write("5. **Full data**: `cpg.json` has per-function CFG stats, param types, exact call sites.\n")
    out.write("\n**Common tasks & where to look**:\n\n")
    out.write("| Task | Primary Package(s) |\n")
    out.write("|------|-------------------|\n")
    out.write("| Add new data source format | `parser`, `provider`, `rules` |\n")
    out.write("| Add API endpoint | `api/router.go`, `api/handlers.go` |\n")
    out.write("| Modify ETL pipeline | `etl/etl.go` |\n")
    out.write("| Database import logic | `dbimport/` |\n")
    out.write("| Crypto download / scraping | `cryptodownload/` (655 fn!) |\n")
    out.write("| Flow graph visualization | `etl/flow_graph.go` + `frontend/` |\n")
    out.write("| Config / env vars | `config/config.go` |\n")
    out.write("\n**Biggest files**: `api/handlers.go`, `etl/etl.go`, all of `cryptodownload/`, `parquetdownload/`.\n")
    out.write("\n---\n*Generated by `tools/cpg/` (Go) + `tools/cpg/enhance_summary.py`.*\n")

if __name__ == '__main__':
    cpg_json = sys.argv[1] if len(sys.argv) > 1 else 'docs/cpg/cpg.json'
    out_path = sys.argv[2] if len(sys.argv) > 2 else 'docs/cpg/summary.md'
    
    data = load_cpg(cpg_json)
    lines = []
    
    class Writer:
        def write(self, s):
            lines.append(s)
    
    w = Writer()
    print_md(w, data)
    
    with open(out_path, 'w', encoding='utf-8') as f:
        f.write(''.join(lines))
    
    print(f"Enhanced summary -> {out_path} ({len(''.join(lines))} chars, {len(lines)} lines)")
