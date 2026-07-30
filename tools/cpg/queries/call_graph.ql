/**
 * @name Extract function call graph
 * @description Maps every caller to every callee with location
 * @kind graph
 * @id cpg/call-graph
 */

import go

from Function caller, Function callee, CallExpr call
where 
  call.getEnclosingFunction() = caller and
  call.getTarget() = callee
select caller.getName() as caller, callee.getName() as callee, 
       call.getLocation().getStartLine() as line
