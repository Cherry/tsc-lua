/*
 * tsc-lua host: LuaJIT with the tsc-lua runtime, the transpiled compiler and
 * the TypeScript lib files embedded as an archive linked into the executable.
 *
 * Archive layout (little endian):
 *   "TSCLUA01" u32 count, then count entries of
 *   u32 nameLen, name bytes, u32 kind (0 = Lua module bytecode, 1 = data file), u32 size, bytes
 */
#include "lauxlib.h"
#include "lua.h"
#include "lualib.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#endif

int luaopen_tscnative(lua_State *L);

/* The archive is linked in with .incbin so it lives in a read-only section of the image. */
#ifndef TSCLUA_ARCHIVE_PATH
#define TSCLUA_ARCHIVE_PATH "build/archive.bin"
#endif
#if defined(__APPLE__)
#define TSCLUA_SECTION ".const_data"
#define TSCLUA_SYM(x) "_" #x
#elif defined(_WIN32)
#define TSCLUA_SECTION ".rdata,\"dr\""
#define TSCLUA_SYM(x) #x
#else
#define TSCLUA_SECTION ".section .rodata"
#define TSCLUA_SYM(x) #x
#endif
#if defined(__APPLE__)
__asm__(TSCLUA_SECTION "\n"
#else
__asm__(".section " TSCLUA_SECTION "\n"
#endif
	".globl " TSCLUA_SYM(tsclua_archive) "\n"
	".balign 16\n"
	TSCLUA_SYM(tsclua_archive) ":\n"
	".incbin \"" TSCLUA_ARCHIVE_PATH "\"\n"
	".globl " TSCLUA_SYM(tsclua_archive_end) "\n"
	TSCLUA_SYM(tsclua_archive_end) ":\n"
	".byte 0\n"
	".text\n");

extern const unsigned char tsclua_archive[];
extern const unsigned char tsclua_archive_end[];

typedef struct {
	const char *name;
	size_t nameLen;
	unsigned kind;
	const unsigned char *data;
	size_t size;
} Entry;

static Entry *entries;
static unsigned entryCount;

static unsigned read_u32(const unsigned char *p) {
	return (unsigned)p[0] | ((unsigned)p[1] << 8) | ((unsigned)p[2] << 16) | ((unsigned)p[3] << 24);
}

static int load_archive(void) {
	const unsigned char *p = tsclua_archive;
	const unsigned char *end = tsclua_archive_end;
	if (end - p < 12 || memcmp(p, "TSCLUA01", 8) != 0) {
		return 0;
	}
	p += 8;
	entryCount = read_u32(p);
	p += 4;
	entries = (Entry *)calloc(entryCount ? entryCount : 1, sizeof(Entry));
	for (unsigned i = 0; i < entryCount; i++) {
		Entry *e = &entries[i];
		e->nameLen = read_u32(p);
		p += 4;
		e->name = (const char *)p;
		p += e->nameLen;
		e->kind = read_u32(p);
		p += 4;
		e->size = read_u32(p);
		p += 4;
		e->data = p;
		p += e->size;
	}
	return 1;
}

static const Entry *find_entry(const char *name, size_t len, unsigned kind) {
	/* Entries are sorted by name at pack time. */
	unsigned lo = 0, hi = entryCount;
	while (lo < hi) {
		unsigned mid = (lo + hi) / 2;
		const Entry *e = &entries[mid];
		size_t n = e->nameLen < len ? e->nameLen : len;
		int c = memcmp(e->name, name, n);
		if (c == 0) {
			c = (e->nameLen > len) - (e->nameLen < len);
		}
		if (c == 0) {
			c = (e->kind > kind) - (e->kind < kind);
		}
		if (c == 0) {
			return e;
		}
		if (c < 0) {
			lo = mid + 1;
		} else {
			hi = mid;
		}
	}
	return NULL;
}

/* package.searchers entry: loads modules from the embedded archive. */
static int archive_searcher(lua_State *L) {
	size_t len;
	const char *name = luaL_checklstring(L, 1, &len);
	const Entry *e = find_entry(name, len, 0);
	if (!e) {
		lua_pushfstring(L, "no module '%s' in tsc-lua archive", name);
		return 1;
	}
	if (luaL_loadbufferx(L, (const char *)e->data, e->size, name, "b") != 0) {
		return lua_error(L);
	}
	lua_pushstring(L, ":archive:");
	return 2;
}

/* tsclua.read(name) returns an embedded data file as a string, or nil. */
static int l_read(lua_State *L) {
	size_t len;
	const char *name = luaL_checklstring(L, 1, &len);
	const Entry *e = find_entry(name, len, 1);
	if (!e) {
		lua_pushnil(L);
		return 1;
	}
	lua_pushlstring(L, (const char *)e->data, e->size);
	return 1;
}

/* tsclua.list(prefix) returns the data file names starting with prefix. */
static int l_list(lua_State *L) {
	size_t plen;
	const char *prefix = luaL_checklstring(L, 1, &plen);
	lua_newtable(L);
	int n = 0;
	for (unsigned i = 0; i < entryCount; i++) {
		const Entry *e = &entries[i];
		if (e->kind == 1 && e->nameLen >= plen && memcmp(e->name, prefix, plen) == 0) {
			lua_pushlstring(L, e->name, e->nameLen);
			lua_rawseti(L, -2, ++n);
		}
	}
	return 1;
}

static int msghandler(lua_State *L) {
	const char *msg = lua_tostring(L, 1);
	if (msg == NULL) {
		if (luaL_callmeta(L, 1, "__tostring") && lua_type(L, -1) == LUA_TSTRING) {
			return 1;
		}
		msg = lua_pushfstring(L, "(error object is a %s value)", luaL_typename(L, 1));
	}
	luaL_traceback(L, L, msg, 1);
	return 1;
}

static int host_panic(lua_State *L) {
	const char *msg = lua_tostring(L, -1);
	fprintf(stderr, "tsc-lua: unprotected error: %s\n", msg ? msg : "(error object is not a string)");
	return 0;
}

static int run(lua_State *L) {
	int argc = (int)lua_tointeger(L, 1);
	char **argv = (char **)lua_touserdata(L, 2);
	luaL_openlibs(L);

	lua_getglobal(L, "package");
	lua_getfield(L, -1, "preload");
	lua_pushcfunction(L, luaopen_tscnative);
	lua_setfield(L, -2, "tscnative");
	lua_pop(L, 1);

	static const luaL_Reg archiveFuncs[] = {{"read", l_read}, {"list", l_list}, {NULL, NULL}};
	luaL_newlib(L, archiveFuncs);
	lua_setglobal(L, "tsclua_archive");

	/* Search the archive first, then the normal searchers (for development overrides). */
	lua_getfield(L, -1, "loaders");
	int n = (int)lua_objlen(L, -1);
	for (int i = n; i >= 2; i--) {
		lua_rawgeti(L, -1, i);
		lua_rawseti(L, -2, i + 1);
	}
	lua_pushcfunction(L, archive_searcher);
	lua_rawseti(L, -2, 2);
	lua_pop(L, 2);

	lua_createtable(L, argc, 1);
	for (int i = 0; i < argc; i++) {
		lua_pushstring(L, argv[i]);
		lua_rawseti(L, -2, i);
	}
	lua_setglobal(L, "arg");

	lua_getglobal(L, "require");
	lua_pushliteral(L, "tsclua_main");
	lua_call(L, 1, 0);
	return 0;
}

#ifdef _WIN32
#include <shellapi.h>
/*
 * The C runtime's argv is in the ANSI code page and glob-expanded by MinGW (a "?" in \\?\ paths
 * matches files), so rebuild it from the UTF-16 command line as UTF-8, unexpanded.
 */
static char **utf8_argv(int *argc) {
	wchar_t **wargv = CommandLineToArgvW(GetCommandLineW(), argc);
	if (wargv == NULL) {
		return NULL;
	}
	char **argv = calloc((size_t)*argc + 1, sizeof(char *));
	for (int i = 0; argv != NULL && i < *argc; i++) {
		int size = WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, NULL, 0, NULL, NULL);
		argv[i] = malloc((size_t)size);
		if (argv[i] == NULL) {
			return NULL;
		}
		WideCharToMultiByte(CP_UTF8, 0, wargv[i], -1, argv[i], size, NULL, NULL);
	}
	LocalFree(wargv);
	return argv;
}
#endif

int main(int argc, char **argv) {
	if (!load_archive()) {
		fprintf(stderr, "tsc-lua: embedded archive is missing or corrupt\n");
		return 2;
	}
#ifdef _WIN32
	int wargc;
	char **wargv = utf8_argv(&wargc);
	if (wargv != NULL) {
		argc = wargc;
		argv = wargv;
	}
#endif
	lua_State *L = luaL_newstate();
	if (L == NULL) {
		fprintf(stderr, "tsc-lua: cannot create Lua state\n");
		return 2;
	}
	lua_atpanic(L, host_panic);
	lua_pushcfunction(L, msghandler);
	lua_pushcfunction(L, run);
	lua_pushinteger(L, argc);
	lua_pushlightuserdata(L, argv);
	int status = lua_pcall(L, 2, 0, 1);
	if (status != 0) {
		const char *msg = lua_tostring(L, -1);
		fprintf(stderr, "tsc-lua: %s\n", msg ? msg : "error");
		lua_close(L);
		return 2;
	}
	lua_close(L);
	return 0;
}
