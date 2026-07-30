/**
 * @name Control Flow Graph nodes
 * @description Extracts all CFG nodes with basic block info
 * @kind table
 * @id cpg/cfg-nodes
 */

import go
import semmle.go.controlflow.ControlFlowGraph

from Function f, ControlFlow::Node n
where n.getEnclosingFunction() = f
select 
  f.getFile().getRelativePath() as file,
  f.getName() as function,
  n.getLocation().getStartLine() as line,
  n.toString() as node_kind
