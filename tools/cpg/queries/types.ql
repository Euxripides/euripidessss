/**
 * @name Type definitions (AST structures)
 * @description Lists all type/struct/interface definitions in the project
 * @kind table
 * @id cpg/types
 */

import go

from TypeSpec ts
select 
  ts.getFile().getRelativePath() as file,
  ts.getName() as type_name,
  ts.getLocation().getStartLine() as line,
  ts.getUnderlyingType().toString() as kind
