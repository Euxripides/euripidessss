package normalize

import "strings"

var methodSignatures = []MethodSignature{
	{MethodID: "0xa9059cbb", Signature: "transfer(address,uint256)", FunctionName: "transfer", Category: "TRANSFER"},
	{MethodID: "0x23b872dd", Signature: "transferFrom(address,address,uint256)", FunctionName: "transferFrom", Category: "TRANSFER"},
	{MethodID: "0x42842e0e", Signature: "safeTransferFrom(address,address,uint256)", FunctionName: "safeTransferFrom", Category: "TRANSFER"},
	{MethodID: "0xb88d4fde", Signature: "safeTransferFrom(address,address,uint256,bytes)", FunctionName: "safeTransferFrom", Category: "TRANSFER"},
	{MethodID: "0x095ea7b3", Signature: "approve(address,uint256)", FunctionName: "approve", Category: "APPROVE"},
	{MethodID: "0xa22cb465", Signature: "setApprovalForAll(address,bool)", FunctionName: "setApprovalForAll", Category: "APPROVE"},
	{MethodID: "0x38ed1739", Signature: "swapExactTokensForTokens(uint256,uint256,address[],address,uint256)", FunctionName: "swapExactTokensForTokens", Category: "SWAP"},
	{MethodID: "0x7ff36ab5", Signature: "swapExactETHForTokens(uint256,address[],address,uint256)", FunctionName: "swapExactETHForTokens", Category: "SWAP"},
	{MethodID: "0x18cbafe5", Signature: "swapExactTokensForETH(uint256,uint256,address[],address,uint256)", FunctionName: "swapExactTokensForETH", Category: "SWAP"},
	{MethodID: "0xa694fc3a", Signature: "stake(uint256)", FunctionName: "stake", Category: "STAKE"},
	{MethodID: "0x40c10f19", Signature: "mint(address,uint256)", FunctionName: "mint", Category: "MINT"},
	{MethodID: "0x42966c68", Signature: "burn(uint256)", FunctionName: "burn", Category: "BURN"},
	{MethodID: "0x4e71d92d", Signature: "claim()", FunctionName: "claim", Category: "CLAIM"},
}

func MethodSignatures() []MethodSignature {
	result := make([]MethodSignature, len(methodSignatures))
	copy(result, methodSignatures)
	return result
}

func ResolveMethod(methodID string) MethodSignature {
	methodID = strings.ToLower(strings.TrimSpace(methodID))
	for _, item := range methodSignatures {
		if item.MethodID == methodID {
			return item
		}
	}
	return MethodSignature{MethodID: methodID, Category: "OTHER"}
}

func ActivityTypeForMethod(methodID, fallback string) string {
	switch ResolveMethod(methodID).Category {
	case "APPROVE":
		return "APPROVE"
	case "SWAP":
		return "SWAP"
	default:
		return fallback
	}
}
