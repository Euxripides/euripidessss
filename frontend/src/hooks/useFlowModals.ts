import { useState } from "react";
import type { RuleAnalysis } from "../types";

export interface UseFlowModalsReturn {
  mappingModalOpen: boolean;
  setMappingModalOpen: (open: boolean) => void;
  ruleOpen: boolean;
  setRuleOpen: (open: boolean) => void;
  pendingRuleAnalysis: RuleAnalysis | null;
  setPendingRuleAnalysis: (analysis: RuleAnalysis | null) => void;
}

export function useFlowModals(): UseFlowModalsReturn {
  const [mappingModalOpen, setMappingModalOpen] = useState(false);
  const [ruleOpen, setRuleOpen] = useState(false);
  const [pendingRuleAnalysis, setPendingRuleAnalysis] = useState<RuleAnalysis | null>(null);

  return {
    mappingModalOpen,
    setMappingModalOpen,
    ruleOpen,
    setRuleOpen,
    pendingRuleAnalysis,
    setPendingRuleAnalysis,
  };
}
