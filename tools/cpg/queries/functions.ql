/**
 * @name Extract all Go functions with signatures
 * @description Lists every function/method with file, line, params, returns for CPG
 * @kind table
 * @id cpg/all-functions
 */

import go

from Function f
select 
  f.getFile().getRelativePath() as file,
  f.getName() as name,
  f.getLocation().getStartLine() as line,
  f.getNumberOfParameters() as param_count,
  f.getNumberOfResults() as result_count,
  f.getBody().(BlockStmt).getAChild().(ExprStmt).getExpr().(CallExpr).getTarget().getName() as first_call
