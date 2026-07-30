/**
 * @name Extract Go package dependencies
 * @description Extracts all package import relationships for the CPG
 * @kind graph
 * @id cpg/package-deps
 */

import go

from Package p, Package q
where p.imports(q)
select p, q
