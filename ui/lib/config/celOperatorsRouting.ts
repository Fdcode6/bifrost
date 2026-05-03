/**
 * CEL Operators Configuration for Routing Rules
 * Maps UI operators to CEL syntax
 */

export interface CELOperatorDefinition {
	name: string;
	label: string;
	celSyntax: string;
}

export const celOperatorsRouting: CELOperatorDefinition[] = [
	// Comparison operators
	{ name: "=", label: "等于", celSyntax: "==" },
	{ name: "!=", label: "不等于", celSyntax: "!=" },
	{ name: ">", label: "大于", celSyntax: ">" },
	{ name: "<", label: "小于", celSyntax: "<" },
	{ name: ">=", label: "大于等于", celSyntax: ">=" },
	{ name: "<=", label: "小于等于", celSyntax: "<=" },

	// List operators
	{ name: "in", label: "在列表中", celSyntax: "in" },
	{ name: "notIn", label: "不在列表中", celSyntax: "!in" },

	// String operators
	{ name: "contains", label: "包含", celSyntax: "contains" },
	{ name: "beginsWith", label: "开头是", celSyntax: "startsWith" },
	{ name: "endsWith", label: "结尾是", celSyntax: "endsWith" },
	{ name: "matches", label: "匹配（regex）", celSyntax: "matches" },

	// Existence operators
	{ name: "null", label: "不存在", celSyntax: "!has" },
	{ name: "notNull", label: "存在", celSyntax: "has" },
];

/**
 * Get CEL syntax for a given operator name
 */
export function getOperatorCELSyntax(operatorName: string): string {
	const operator = celOperatorsRouting.find((op) => op.name === operatorName);
	return operator ? operator.celSyntax : operatorName;
}

/**
 * Get operator label for display
 */
export function getOperatorLabel(operatorName: string): string {
	const operator = celOperatorsRouting.find((op) => op.name === operatorName);
	return operator ? operator.label : operatorName;
}
