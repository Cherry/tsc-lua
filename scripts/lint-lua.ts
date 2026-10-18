// Lints the hand written Lua sources: undefined globals and unused locals (mirroring ESLint's
// no-undef and no-unused-vars), with formatting checked (or applied with --fix) by StyLua.
import { spawnSync } from 'node:child_process';
import fs from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

import luaparse from 'luaparse';

const root = path.resolve(import.meta.dirname, '..');
const require = createRequire(import.meta.url);
const fix = process.argv.includes('--fix');

const sourceDirs = ['runtime', 'scripts'];

const knownGlobals = new Set([
	'_G',
	'_VERSION',
	'arg',
	'assert',
	'bit',
	'collectgarbage',
	'coroutine',
	'debug',
	'dofile',
	'error',
	'getmetatable',
	'io',
	'ipairs',
	'jit',
	'load',
	'loadfile',
	'math',
	'next',
	'os',
	'package',
	'pairs',
	'pcall',
	'print',
	'rawequal',
	'rawget',
	'rawlen',
	'rawset',
	'require',
	'select',
	'setmetatable',
	'string',
	'table',
	'tonumber',
	'tostring',
	'type',
	'unpack',
	'xpcall',
	// Provided by the tsc-lua executable (native/host.c).
	'tsclua_archive',
]);

interface Problem {
	file: string;
	line: number;
	column: number;
	message: string;
}

interface Binding {
	name: string;
	line: number;
	column: number;
	used: boolean;
	kind: 'local' | 'param' | 'function';
}

type Node = luaparse.Node & { loc?: { start: { line: number; column: number; }; }; };

function collectFiles(dir: string, out: string[]): void {
	for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
		const full = path.join(dir, entry.name);
		if (entry.isDirectory()) {
			collectFiles(full, out);
		} else if (entry.name.endsWith('.lua')) {
			out.push(full);
		}
	}
}

export function lintSource(file: string, source: string): Problem[] {
	const problems: Problem[] = [];
	const report = (node: Node | Binding, message: string) => {
		const line = 'loc' in node && node.loc ? node.loc.start.line : (node as Binding).line;
		const column = 'loc' in node && node.loc ? node.loc.start.column + 1 : (node as Binding).column;
		problems.push({ file, line, column, message });
	};

	let ast: luaparse.Chunk;
	try {
		ast = luaparse.parse(source, { locations: true, luaVersion: 'LuaJIT', comments: false });
	} catch (error) {
		const err = error as { line?: number; column?: number; message: string; };
		return [{ file, line: err.line ?? 1, column: (err.column ?? 0) + 1, message: `parse error: ${err.message}` }];
	}

	const scopes: Map<string, Binding>[] = [];
	const pushScope = () => {
		scopes.push(new Map());
	};
	const currentScope = (): Map<string, Binding> => {
		const scope = scopes.at(-1);
		if (!scope) {
			throw new Error('lint-lua: no active scope');
		}
		return scope;
	};
	// Leading positional bindings (parameters, loop variables) are used if a later one is.
	const markLeadingUsed = (names: string[]) => {
		const scope = currentScope();
		let lastUsed = -1;
		for (const [index, name] of names.entries()) {
			if (scope.get(name)?.used) {
				lastUsed = index;
			}
		}
		for (const [index, name] of names.entries()) {
			const binding = scope.get(name);
			if (binding && index < lastUsed) {
				binding.used = true;
			}
		}
	};
	const popScope = () => {
		const scope = scopes.pop();
		if (!scope) {
			return;
		}
		for (const binding of scope.values()) {
			if (!binding.used && !binding.name.startsWith('_')) {
				report(binding, `'${binding.name}' is ${binding.kind === 'param' ? 'an unused parameter' : 'assigned but never used'}`);
			}
		}
	};
	const declare = (id: luaparse.Identifier, kind: Binding['kind']) => {
		const loc = (id as Node).loc;
		const current = scopes.at(-1);
		const previous = current?.get(id.name);
		// Redeclaring in the same scope shadows; report the earlier one if it was never used.
		if (previous && !previous.used && !previous.name.startsWith('_')) {
			report(previous, `'${previous.name}' is assigned but never used`);
		}
		current?.set(id.name, { name: id.name, line: loc?.start.line ?? 0, column: (loc?.start.column ?? 0) + 1, used: false, kind });
	};
	const resolve = (name: string): Binding | undefined => {
		for (let i = scopes.length - 1; i >= 0; i--) {
			const binding = scopes[i].get(name);
			if (binding) {
				return binding;
			}
		}
		return undefined;
	};
	const useIdentifier = (id: luaparse.Identifier) => {
		const binding = resolve(id.name);
		if (binding) {
			binding.used = true;
		} else if (!knownGlobals.has(id.name)) {
			report(id as Node, `'${id.name}' is not defined`);
		}
	};

	function visitBlock(body: luaparse.Statement[]) {
		for (const statement of body) {
			// eslint-disable-next-line @typescript-eslint/no-use-before-define -- mutually recursive with visit
			visit(statement);
		}
	}
	function visitFunction(node: luaparse.FunctionDeclaration) {
		pushScope();
		const params: string[] = [];
		for (const param of node.parameters) {
			if (param.type !== 'Identifier') {
				continue;
			}
			declare(param, 'param');
			params.push(param.name);
		}
		visitBlock(node.body);
		markLeadingUsed(params);
		popScope();
	}

	function visit(node: luaparse.Node | null | undefined): void {
		if (!node) {
			return;
		}
		switch (node.type) {
			case 'LocalStatement': {
				for (const init of node.init) {
					visit(init);
				}
				for (const variable of node.variables) {
					declare(variable, 'local');
				}
				break;
			}
			case 'AssignmentStatement': {
				for (const init of node.init) {
					visit(init);
				}
				for (const variable of node.variables) {
					if (variable.type === 'Identifier') {
						if (!resolve(variable.name) && !knownGlobals.has(variable.name)) {
							report(variable as Node, `assignment to undeclared global '${variable.name}'`);
						}
					} else {
						visit(variable);
					}
				}
				break;
			}
			case 'FunctionDeclaration': {
				if (node.identifier && node.identifier.type === 'Identifier') {
					if (node.isLocal) {
						declare(node.identifier, 'function');
					} else if (!resolve(node.identifier.name)) {
						report(node.identifier as Node, `global function '${node.identifier.name}'`);
					}
				} else if (node.identifier) {
					visit(node.identifier);
				}
				visitFunction(node);
				break;
			}
			case 'Identifier': {
				useIdentifier(node);
				break;
			}
			case 'MemberExpression': {
				visit(node.base);
				break;
			}
			case 'IndexExpression': {
				visit(node.base);
				visit(node.index);
				break;
			}
			case 'CallExpression': {
				visit(node.base);
				for (const argument of node.arguments) {
					visit(argument);
				}
				break;
			}
			case 'TableCallExpression': {
				visit(node.base);
				visit(node.arguments);
				break;
			}
			case 'StringCallExpression': {
				visit(node.base);
				break;
			}
			case 'CallStatement': {
				visit(node.expression);
				break;
			}
			case 'BinaryExpression':
			case 'LogicalExpression': {
				visit(node.left);
				visit(node.right);
				break;
			}
			case 'UnaryExpression': {
				visit(node.argument);
				break;
			}
			case 'TableConstructorExpression': {
				for (const field of node.fields) {
					if (field.type === 'TableKey') {
						visit(field.key);
					}
					visit(field.value);
				}
				break;
			}
			case 'ReturnStatement': {
				for (const argument of node.arguments) {
					visit(argument);
				}
				break;
			}
			case 'IfStatement': {
				for (const clause of node.clauses) {
					if (clause.type !== 'ElseClause') {
						visit(clause.condition);
					}
					pushScope();
					visitBlock(clause.body);
					popScope();
				}
				break;
			}
			case 'WhileStatement': {
				visit(node.condition);
				pushScope();
				visitBlock(node.body);
				popScope();
				break;
			}
			case 'RepeatStatement': {
				pushScope();
				visitBlock(node.body);
				visit(node.condition);
				popScope();
				break;
			}
			case 'DoStatement': {
				pushScope();
				visitBlock(node.body);
				popScope();
				break;
			}
			case 'ForNumericStatement': {
				visit(node.start);
				visit(node.end);
				visit(node.step);
				pushScope();
				declare(node.variable, 'local');
				// Numeric loop counters are often unused by design (repeat N times).
				const counter = currentScope().get(node.variable.name);
				if (counter) {
					counter.used = true;
				}
				visitBlock(node.body);
				popScope();
				break;
			}
			case 'ForGenericStatement': {
				for (const iterator of node.iterators) {
					visit(iterator);
				}
				pushScope();
				for (const variable of node.variables) {
					declare(variable, 'param');
				}
				visitBlock(node.body);
				markLeadingUsed(node.variables.map(variable => variable.name));
				popScope();
				break;
			}
			default: {
				break;
			}
		}
	}

	pushScope();
	visitBlock(ast.body);
	popScope();
	return problems;
}

function runStylua(files: string[]): boolean {
	const stylua = require.resolve('@johnnymorganz/stylua-bin/run.js');
	const args = fix ? files : ['--check', ...files];
	const result = spawnSync(process.execPath, [stylua, ...args], { cwd: root, stdio: 'inherit' });
	return result.status === 0;
}

function main(): void {
	const files: string[] = [];
	for (const dir of sourceDirs) {
		collectFiles(path.join(root, dir), files);
	}
	const formatted = runStylua(files);
	let count = 0;
	for (const file of files) {
		for (const problem of lintSource(path.relative(root, file), fs.readFileSync(file, 'utf8'))) {
			console.log(`${problem.file}:${problem.line}:${problem.column}  ${problem.message}`);
			count++;
		}
	}
	if (count > 0) {
		console.log(`\n${count} problem${count === 1 ? '' : 's'}`);
	}
	if (count > 0 || !formatted) {
		process.exitCode = 1;
	}
}

if (import.meta.main) {
	main();
}
