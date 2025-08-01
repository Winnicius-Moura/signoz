import { NON_VALUE_OPERATORS } from 'constants/antlrQueryConstants';
import FilterQueryLexer from 'parser/FilterQueryLexer';
import { IQueryPair } from 'types/antlrQueryTypes';

export function isKeyToken(tokenType: number): boolean {
	return tokenType === FilterQueryLexer.KEY;
}

// Helper function to check if a token is an operator
export function isOperatorToken(tokenType: number): boolean {
	return [
		FilterQueryLexer.EQUALS,
		FilterQueryLexer.NOT_EQUALS,
		FilterQueryLexer.NEQ,
		FilterQueryLexer.LT,
		FilterQueryLexer.LE,
		FilterQueryLexer.GT,
		FilterQueryLexer.GE,
		FilterQueryLexer.LIKE,
		FilterQueryLexer.ILIKE,
		FilterQueryLexer.BETWEEN,
		FilterQueryLexer.EXISTS,
		FilterQueryLexer.REGEXP,
		FilterQueryLexer.CONTAINS,
		FilterQueryLexer.IN,
		FilterQueryLexer.NOT,
	].includes(tokenType);
}

// Helper function to check if a token is an operator which doesn't require a value
export function isNonValueOperatorToken(tokenType: number): boolean {
	return [FilterQueryLexer.EXISTS].includes(tokenType);
}

// Helper function to check if a token is a value
export function isValueToken(tokenType: number): boolean {
	return [
		FilterQueryLexer.QUOTED_TEXT,
		FilterQueryLexer.NUMBER,
		FilterQueryLexer.BOOL,
		FilterQueryLexer.KEY,
	].includes(tokenType);
}

// Helper function to check if a token is a conjunction
export function isConjunctionToken(tokenType: number): boolean {
	return [FilterQueryLexer.AND, FilterQueryLexer.OR].includes(tokenType);
}

// Helper function to check if a token is a bracket
export function isBracketToken(tokenType: number): boolean {
	return [
		FilterQueryLexer.LPAREN,
		FilterQueryLexer.RPAREN,
		FilterQueryLexer.LBRACK,
		FilterQueryLexer.RBRACK,
	].includes(tokenType);
}

// Helper function to check if an operator typically uses bracket values (multi-value operators)
export function isMultiValueOperator(operatorToken?: string): boolean {
	if (!operatorToken) return false;

	const upperOp = operatorToken.toUpperCase();
	return upperOp === 'IN';
}

export function isFunctionToken(tokenType: number): boolean {
	return [
		FilterQueryLexer.HAS,
		FilterQueryLexer.HASANY,
		FilterQueryLexer.HASALL,
	].includes(tokenType);
}

export function isWrappedUnderQuotes(token: string): boolean {
	if (!token) return false;
	const sanitizedToken = token.trim();
	return (
		(sanitizedToken.startsWith('"') && sanitizedToken.endsWith('"')) ||
		(sanitizedToken.startsWith("'") && sanitizedToken.endsWith("'"))
	);
}

export function isQueryPairComplete(queryPair: Partial<IQueryPair>): boolean {
	if (!queryPair) return false;
	// A complete query pair must have a key, an operator, and a value (or EXISTS operator)
	if (queryPair.operator && NON_VALUE_OPERATORS.includes(queryPair.operator)) {
		return !!queryPair.key && !!queryPair.operator;
	}
	// For other operators, we need a value as well
	return Boolean(queryPair.key && queryPair.operator && queryPair.value);
}
