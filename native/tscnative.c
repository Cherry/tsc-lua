/*
 * tscnative: the small set of OS services tsc-lua needs that plain Lua lacks
 * (directory listing, stat, realpath, high resolution clocks, tty detection).
 * Errors are returned as nil, message, code where code is an errno-like name.
 */
#ifndef _WIN32
/* dladdr and RTLD_NODELETE (see l_setmemlimit). */
#define _GNU_SOURCE
#endif
#define LUA_LIB
#include "lua.h"
#include "lauxlib.h"
#include "lualib.h"
#ifdef _WIN32
/* LuaJIT internals, to create io file handles (see l_open). */
#include "lj_obj.h"
#endif

#ifndef LUAMOD_API
#define LUAMOD_API LUALIB_API
#endif

#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
#ifndef _WIN32_WINNT
#define _WIN32_WINNT 0x0A00
#endif
#define WIN32_LEAN_AND_MEAN
#include <windows.h>
#include <io.h>
#ifndef SYMBOLIC_LINK_FLAG_DIRECTORY
#define SYMBOLIC_LINK_FLAG_DIRECTORY 0x1
#endif
#ifndef SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE
#define SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE 0x2
#endif
#ifdef __MINGW32__
/* Older MinGW headers lack these Vista+ prototypes. */
WINBASEAPI DWORD WINAPI GetFinalPathNameByHandleW(HANDLE, LPWSTR, DWORD, DWORD);
WINBASEAPI BOOLEAN WINAPI CreateSymbolicLinkW(LPCWSTR, LPCWSTR, DWORD);
#endif
#else
#include <dirent.h>
#include <dlfcn.h>
#include <limits.h>
#include <sys/stat.h>
#include <sys/time.h>
#include <sys/types.h>
#include <unistd.h>
#ifdef __APPLE__
#include <mach-o/dyld.h>
#endif
#endif

#ifdef _WIN32

static wchar_t *to_wide(const char *s) {
	int n = MultiByteToWideChar(CP_UTF8, 0, s, -1, NULL, 0);
	wchar_t *w = (wchar_t *)malloc(sizeof(wchar_t) * (n > 0 ? n : 1));
	if (n > 0) {
		MultiByteToWideChar(CP_UTF8, 0, s, -1, w, n);
	} else {
		w[0] = 0;
	}
	return w;
}

/* Like Go's fixLongPath: paths of 248+ characters need the \\?\ form of their full path. */
static wchar_t *to_wide_path(const char *s) {
	wchar_t *w = to_wide(s);
	if (wcslen(w) < 248 || wcsncmp(w, L"\\\\?\\", 4) == 0) {
		return w;
	}
	DWORD n = GetFullPathNameW(w, 0, NULL, NULL);
	wchar_t *full = n > 0 ? (wchar_t *)malloc(sizeof(wchar_t) * n) : NULL;
	if (full == NULL || GetFullPathNameW(w, n, full, NULL) == 0) {
		free(full);
		return w;
	}
	int unc = full[0] == L'\\' && full[1] == L'\\';
	const wchar_t *prefix = unc ? L"\\\\?\\UNC\\" : L"\\\\?\\";
	const wchar_t *rest = unc ? full + 2 : full;
	size_t prefixLen = wcslen(prefix);
	size_t restLen = wcslen(rest) + 1;
	wchar_t *out = (wchar_t *)malloc(sizeof(wchar_t) * (prefixLen + restLen));
	if (out != NULL) {
		memcpy(out, prefix, sizeof(wchar_t) * prefixLen);
		memcpy(out + prefixLen, rest, sizeof(wchar_t) * restLen);
	}
	free(full);
	if (out == NULL) {
		return w;
	}
	free(w);
	return out;
}

static void push_utf8(lua_State *L, const wchar_t *w) {
	int n = WideCharToMultiByte(CP_UTF8, 0, w, -1, NULL, 0, NULL, NULL);
	if (n <= 0) {
		lua_pushliteral(L, "");
		return;
	}
	char *s = (char *)malloc(n);
	WideCharToMultiByte(CP_UTF8, 0, w, -1, s, n, NULL, NULL);
	lua_pushstring(L, s);
	free(s);
}

static const char *win_code(DWORD err) {
	switch (err) {
	case ERROR_FILE_NOT_FOUND:
	case ERROR_PATH_NOT_FOUND:
	case ERROR_INVALID_NAME:
	case ERROR_BAD_NETPATH:
	case ERROR_INVALID_DRIVE:
		return "ENOENT";
	case ERROR_ALREADY_EXISTS:
	case ERROR_FILE_EXISTS:
		return "EEXIST";
	case ERROR_ACCESS_DENIED:
	case ERROR_SHARING_VIOLATION:
		return "EACCES";
	case ERROR_DIRECTORY:
		return "ENOTDIR";
	case ERROR_DIR_NOT_EMPTY:
		return "ENOTEMPTY";
	default:
		return "EIO";
	}
}

static int push_win_error(lua_State *L, const char *path) {
	DWORD err = GetLastError();
	char buf[512];
	DWORD n = FormatMessageA(FORMAT_MESSAGE_FROM_SYSTEM | FORMAT_MESSAGE_IGNORE_INSERTS, NULL, err, 0, buf, sizeof(buf), NULL);
	while (n > 0 && (buf[n - 1] == '\n' || buf[n - 1] == '\r' || buf[n - 1] == '.')) {
		buf[--n] = 0;
	}
	lua_pushnil(L);
	if (path) {
		lua_pushfstring(L, "%s", n > 0 ? buf : "error");
	} else {
		lua_pushstring(L, n > 0 ? buf : "error");
	}
	lua_pushstring(L, win_code(err));
	return 3;
}

/* FILETIME (100ns since 1601) to unix seconds/nanoseconds */
static void filetime_to_unix(FILETIME ft, lua_Integer *sec, lua_Integer *nsec) {
	ULARGE_INTEGER u;
	u.LowPart = ft.dwLowDateTime;
	u.HighPart = ft.dwHighDateTime;
	unsigned long long t = u.QuadPart - 116444736000000000ULL;
	*sec = (lua_Integer)(t / 10000000ULL);
	*nsec = (lua_Integer)((t % 10000000ULL) * 100);
}

static int push_attr_stat(lua_State *L, DWORD attrs, FILETIME mtime, DWORD sizeHigh, DWORD sizeLow) {
	lua_Integer sec, nsec;
	lua_createtable(L, 0, 6);
	lua_pushboolean(L, (attrs & FILE_ATTRIBUTE_DIRECTORY) != 0);
	lua_setfield(L, -2, "isdir");
	lua_pushboolean(L, (attrs & FILE_ATTRIBUTE_REPARSE_POINT) != 0);
	lua_setfield(L, -2, "islink");
	lua_pushinteger(L, ((lua_Integer)sizeHigh << 32) | sizeLow);
	lua_setfield(L, -2, "size");
	filetime_to_unix(mtime, &sec, &nsec);
	lua_pushinteger(L, sec);
	lua_setfield(L, -2, "mtime");
	lua_pushinteger(L, nsec);
	lua_setfield(L, -2, "mtimensec");
	lua_pushinteger(L, (attrs & FILE_ATTRIBUTE_READONLY) ? 0444 : 0666);
	lua_setfield(L, -2, "perm");
	return 1;
}

static int do_stat(lua_State *L, int follow) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	WIN32_FILE_ATTRIBUTE_DATA data;
	if (!GetFileAttributesExW(w, GetFileExInfoStandard, &data)) {
		free(w);
		return push_win_error(L, path);
	}
	if (follow && (data.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT)) {
		HANDLE h = CreateFileW(w, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
		if (h == INVALID_HANDLE_VALUE) {
			free(w);
			return push_win_error(L, path);
		}
		BY_HANDLE_FILE_INFORMATION info;
		BOOL ok = GetFileInformationByHandle(h, &info);
		CloseHandle(h);
		free(w);
		if (!ok) {
			return push_win_error(L, path);
		}
		push_attr_stat(L, info.dwFileAttributes & ~FILE_ATTRIBUTE_REPARSE_POINT, info.ftLastWriteTime, info.nFileSizeHigh, info.nFileSizeLow);
		return 1;
	}
	free(w);
	return push_attr_stat(L, data.dwFileAttributes, data.ftLastWriteTime, data.nFileSizeHigh, data.nFileSizeLow);
}

static int l_readdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	size_t len = strlen(path);
	char *pattern = (char *)malloc(len + 3);
	memcpy(pattern, path, len);
	if (len > 0 && path[len - 1] != '/' && path[len - 1] != '\\') {
		pattern[len++] = '/';
	}
	pattern[len++] = '*';
	pattern[len] = 0;
	wchar_t *w = to_wide_path(pattern);
	free(pattern);
	WIN32_FIND_DATAW fd;
	HANDLE h = FindFirstFileExW(w, FindExInfoBasic, &fd, FindExSearchNameMatch, NULL, FIND_FIRST_EX_LARGE_FETCH);
	free(w);
	if (h == INVALID_HANDLE_VALUE) {
		if (GetLastError() == ERROR_FILE_NOT_FOUND) {
			lua_newtable(L);
			return 1;
		}
		return push_win_error(L, path);
	}
	lua_newtable(L);
	int i = 0;
	do {
		if (wcscmp(fd.cFileName, L".") == 0 || wcscmp(fd.cFileName, L"..") == 0) {
			continue;
		}
		lua_createtable(L, 2, 0);
		push_utf8(L, fd.cFileName);
		lua_rawseti(L, -2, 1);
		const char *type = "f";
		if (fd.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) {
			type = "l";
		} else if (fd.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) {
			type = "d";
		}
		lua_pushstring(L, type);
		lua_rawseti(L, -2, 2);
		lua_rawseti(L, -2, ++i);
	} while (FindNextFileW(h, &fd));
	FindClose(h);
	return 1;
}

static int l_mkdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	BOOL ok = CreateDirectoryW(w, NULL);
	free(w);
	if (!ok) {
		return push_win_error(L, path);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_rmdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	BOOL ok = RemoveDirectoryW(w);
	free(w);
	if (!ok) {
		return push_win_error(L, path);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_unlink(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	BOOL ok = DeleteFileW(w);
	free(w);
	if (!ok) {
		return push_win_error(L, path);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_rename(lua_State *L) {
	const char *from = luaL_checkstring(L, 1);
	const char *to = luaL_checkstring(L, 2);
	wchar_t *wf = to_wide_path(from);
	wchar_t *wt = to_wide_path(to);
	BOOL ok = MoveFileExW(wf, wt, MOVEFILE_REPLACE_EXISTING);
	free(wf);
	free(wt);
	if (!ok) {
		return push_win_error(L, from);
	}
	lua_pushboolean(L, 1);
	return 1;
}

/*
 * io.open with a UTF-8 (and long) path: the C runtime's fopen takes ANSI code page paths. The
 * handle must be one of LuaJIT's own io file objects, which its io functions recognize by the
 * userdata type, so it is built as lib_io.c's io_file_new does.
 */
typedef struct {
	FILE *fp;
	uint32_t type;
} IOFileUD;

static int l_open(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	const char *mode = luaL_optstring(L, 2, "r");
	/* Same rule as lib_io.c's io_file_open. */
	const char *m = mode;
	int valid = *m != '\0' && strchr("rwa", *m++) != NULL;
	if (valid && *m == '+') {
		m++;
	}
	valid = valid && strspn(m, "b") == strlen(m);
	luaL_argcheck(L, valid, 2, "invalid mode");
	wchar_t *w = to_wide_path(path);
	wchar_t *wmode = to_wide(mode);
	FILE *fp = _wfopen(w, wmode);
	free(w);
	free(wmode);
	if (fp == NULL) {
		return luaL_fileresult(L, 0, path);
	}
	IOFileUD *iof = (IOFileUD *)lua_newuserdata(L, sizeof(IOFileUD));
	udataV(L->top - 1)->udtype = UDTYPE_IO_FILE;
	iof->fp = fp;
	iof->type = 0; /* IOFILE_TYPE_FILE */
	luaL_getmetatable(L, LUA_FILEHANDLE);
	lua_setmetatable(L, -2);
	return 1;
}

static int l_realpath(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	HANDLE h = CreateFileW(w, 0, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
	free(w);
	if (h == INVALID_HANDLE_VALUE) {
		return push_win_error(L, path);
	}
	wchar_t buf[32768];
	DWORD n = GetFinalPathNameByHandleW(h, buf, 32768, FILE_NAME_NORMALIZED);
	CloseHandle(h);
	if (n == 0 || n >= 32768) {
		return push_win_error(L, path);
	}
	wchar_t *p = buf;
	if (wcsncmp(p, L"\\\\?\\UNC\\", 8) == 0) {
		p += 6;
		p[0] = L'\\';
	} else if (wcsncmp(p, L"\\\\?\\", 4) == 0) {
		p += 4;
	}
	push_utf8(L, p);
	return 1;
}

static int l_getcwd(lua_State *L) {
	wchar_t buf[32768];
	DWORD n = GetCurrentDirectoryW(32768, buf);
	if (n == 0) {
		return push_win_error(L, NULL);
	}
	push_utf8(L, buf);
	return 1;
}

static int l_chdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	wchar_t *w = to_wide_path(path);
	BOOL ok = SetCurrentDirectoryW(w);
	free(w);
	if (!ok) {
		return push_win_error(L, path);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_monotime(lua_State *L) {
	static LARGE_INTEGER freq;
	LARGE_INTEGER now;
	if (freq.QuadPart == 0) {
		QueryPerformanceFrequency(&freq);
	}
	QueryPerformanceCounter(&now);
	lua_pushnumber(L, (lua_Number)now.QuadPart / (lua_Number)freq.QuadPart);
	return 1;
}

static int l_walltime(lua_State *L) {
	FILETIME ft;
	lua_Integer sec, nsec;
	GetSystemTimePreciseAsFileTime(&ft);
	filetime_to_unix(ft, &sec, &nsec);
	lua_pushinteger(L, sec);
	lua_pushinteger(L, nsec);
	return 2;
}

static int l_sleep(lua_State *L) {
	lua_Number s = luaL_checknumber(L, 1);
	if (s > 0) {
		Sleep((DWORD)(s * 1000));
	}
	return 0;
}

static int l_isatty(lua_State *L) {
	int fd = (int)luaL_checkinteger(L, 1);
	lua_pushboolean(L, _isatty(fd));
	return 1;
}

static int l_exepath(lua_State *L) {
	wchar_t buf[32768];
	DWORD n = GetModuleFileNameW(NULL, buf, 32768);
	if (n == 0) {
		return push_win_error(L, NULL);
	}
	push_utf8(L, buf);
	return 1;
}

static int l_readlink(lua_State *L) {
	return l_realpath(L);
}

static int l_symlink(lua_State *L) {
	const char *target = luaL_checkstring(L, 1);
	const char *link = luaL_checkstring(L, 2);
	wchar_t *wt = to_wide(target);
	wchar_t *wl = to_wide_path(link);
	DWORD attrs = GetFileAttributesW(wt);
	DWORD flags = SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE;
	if (attrs != INVALID_FILE_ATTRIBUTES && (attrs & FILE_ATTRIBUTE_DIRECTORY)) {
		flags |= SYMBOLIC_LINK_FLAG_DIRECTORY;
	}
	BOOLEAN ok = CreateSymbolicLinkW(wl, wt, flags);
	free(wt);
	free(wl);
	if (!ok) {
		return push_win_error(L, link);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_utimes(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	lua_Integer sec = luaL_checkinteger(L, 2);
	lua_Integer nsec = luaL_optinteger(L, 3, 0);
	wchar_t *w = to_wide_path(path);
	HANDLE h = CreateFileW(w, FILE_WRITE_ATTRIBUTES, FILE_SHARE_READ | FILE_SHARE_WRITE, NULL, OPEN_EXISTING, FILE_FLAG_BACKUP_SEMANTICS, NULL);
	free(w);
	if (h == INVALID_HANDLE_VALUE) {
		return push_win_error(L, path);
	}
	unsigned long long t = (unsigned long long)sec * 10000000ULL + (unsigned long long)nsec / 100 + 116444736000000000ULL;
	FILETIME ft;
	ft.dwLowDateTime = (DWORD)t;
	ft.dwHighDateTime = (DWORD)(t >> 32);
	BOOL ok = SetFileTime(h, NULL, &ft, &ft);
	CloseHandle(h);
	if (!ok) {
		return push_win_error(L, path);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_cpucount(lua_State *L) {
	SYSTEM_INFO si;
	GetSystemInfo(&si);
	lua_pushinteger(L, si.dwNumberOfProcessors);
	return 1;
}

static int l_setbinary(lua_State *L) {
	(void)L;
	_setmode(_fileno(stdout), 0x8000 /* _O_BINARY */);
	_setmode(_fileno(stderr), 0x8000);
	_setmode(_fileno(stdin), 0x8000);
	return 0;
}

static int l_enablevt(lua_State *L) {
	HANDLE h = GetStdHandle(STD_OUTPUT_HANDLE);
	DWORD mode = 0;
	if (h != INVALID_HANDLE_VALUE && GetConsoleMode(h, &mode)) {
		SetConsoleMode(h, mode | 0x0004 /* ENABLE_VIRTUAL_TERMINAL_PROCESSING */);
	}
	SetConsoleOutputCP(CP_UTF8);
	return 0;
}

#else /* POSIX */

static const char *posix_code(int err) {
	switch (err) {
	case ENOENT:
		return "ENOENT";
	case EEXIST:
		return "EEXIST";
	case EACCES:
	case EPERM:
		return "EACCES";
	case ENOTDIR:
		return "ENOTDIR";
	case EISDIR:
		return "EISDIR";
	case ENOTEMPTY:
		return "ENOTEMPTY";
	case ELOOP:
		return "ELOOP";
	default:
		return "EIO";
	}
}

static int push_errno(lua_State *L) {
	int err = errno;
	lua_pushnil(L);
	lua_pushstring(L, strerror(err));
	lua_pushstring(L, posix_code(err));
	return 3;
}

static int push_stat(lua_State *L, struct stat *st) {
	lua_createtable(L, 0, 6);
	lua_pushboolean(L, S_ISDIR(st->st_mode));
	lua_setfield(L, -2, "isdir");
	lua_pushboolean(L, S_ISLNK(st->st_mode));
	lua_setfield(L, -2, "islink");
	lua_pushinteger(L, (lua_Integer)st->st_size);
	lua_setfield(L, -2, "size");
#if defined(__APPLE__)
	lua_pushinteger(L, (lua_Integer)st->st_mtimespec.tv_sec);
	lua_setfield(L, -2, "mtime");
	lua_pushinteger(L, (lua_Integer)st->st_mtimespec.tv_nsec);
	lua_setfield(L, -2, "mtimensec");
#else
	lua_pushinteger(L, (lua_Integer)st->st_mtim.tv_sec);
	lua_setfield(L, -2, "mtime");
	lua_pushinteger(L, (lua_Integer)st->st_mtim.tv_nsec);
	lua_setfield(L, -2, "mtimensec");
#endif
	lua_pushinteger(L, (lua_Integer)(st->st_mode & 07777));
	lua_setfield(L, -2, "perm");
	return 1;
}

static int do_stat(lua_State *L, int follow) {
	const char *path = luaL_checkstring(L, 1);
	struct stat st;
	int r = follow ? stat(path, &st) : lstat(path, &st);
	if (r != 0) {
		return push_errno(L);
	}
	return push_stat(L, &st);
}

static int l_readdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	DIR *d = opendir(path);
	if (!d) {
		return push_errno(L);
	}
	lua_newtable(L);
	int i = 0;
	struct dirent *e;
	while ((e = readdir(d)) != NULL) {
		if (strcmp(e->d_name, ".") == 0 || strcmp(e->d_name, "..") == 0) {
			continue;
		}
		lua_createtable(L, 2, 0);
		lua_pushstring(L, e->d_name);
		lua_rawseti(L, -2, 1);
		const char *type = "?";
#ifdef DT_DIR
		if (e->d_type == DT_DIR) {
			type = "d";
		} else if (e->d_type == DT_REG) {
			type = "f";
		} else if (e->d_type == DT_LNK) {
			type = "l";
		}
#endif
		lua_pushstring(L, type);
		lua_rawseti(L, -2, 2);
		lua_rawseti(L, -2, ++i);
	}
	closedir(d);
	return 1;
}

static int l_mkdir(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	lua_Integer mode = luaL_optinteger(L, 2, 0777);
	if (mkdir(path, (mode_t)mode) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_rmdir(lua_State *L) {
	if (rmdir(luaL_checkstring(L, 1)) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_unlink(lua_State *L) {
	if (unlink(luaL_checkstring(L, 1)) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_rename(lua_State *L) {
	if (rename(luaL_checkstring(L, 1), luaL_checkstring(L, 2)) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_realpath(lua_State *L) {
	char buf[PATH_MAX];
	if (!realpath(luaL_checkstring(L, 1), buf)) {
		return push_errno(L);
	}
	lua_pushstring(L, buf);
	return 1;
}

static int l_getcwd(lua_State *L) {
	char buf[PATH_MAX];
	if (!getcwd(buf, sizeof(buf))) {
		return push_errno(L);
	}
	lua_pushstring(L, buf);
	return 1;
}

static int l_chdir(lua_State *L) {
	if (chdir(luaL_checkstring(L, 1)) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_monotime(lua_State *L) {
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	lua_pushnumber(L, (lua_Number)ts.tv_sec + (lua_Number)ts.tv_nsec / 1e9);
	return 1;
}

static int l_walltime(lua_State *L) {
	struct timespec ts;
	clock_gettime(CLOCK_REALTIME, &ts);
	lua_pushinteger(L, (lua_Integer)ts.tv_sec);
	lua_pushinteger(L, (lua_Integer)ts.tv_nsec);
	return 2;
}

static int l_sleep(lua_State *L) {
	lua_Number s = luaL_checknumber(L, 1);
	if (s > 0) {
		struct timespec ts;
		ts.tv_sec = (time_t)s;
		ts.tv_nsec = (long)((s - (lua_Number)ts.tv_sec) * 1e9);
		nanosleep(&ts, NULL);
	}
	return 0;
}

static int l_isatty(lua_State *L) {
	lua_pushboolean(L, isatty((int)luaL_checkinteger(L, 1)));
	return 1;
}

static int l_exepath(lua_State *L) {
	char buf[PATH_MAX];
#ifdef __APPLE__
	uint32_t size = sizeof(buf);
	if (_NSGetExecutablePath(buf, &size) != 0) {
		lua_pushnil(L);
		return 1;
	}
	char real[PATH_MAX];
	if (realpath(buf, real)) {
		lua_pushstring(L, real);
		return 1;
	}
	lua_pushstring(L, buf);
	return 1;
#else
	ssize_t n = readlink("/proc/self/exe", buf, sizeof(buf) - 1);
	if (n < 0) {
		return push_errno(L);
	}
	buf[n] = 0;
	lua_pushstring(L, buf);
	return 1;
#endif
}

static int l_readlink(lua_State *L) {
	char buf[PATH_MAX];
	ssize_t n = readlink(luaL_checkstring(L, 1), buf, sizeof(buf) - 1);
	if (n < 0) {
		return push_errno(L);
	}
	buf[n] = 0;
	lua_pushstring(L, buf);
	return 1;
}

static int l_symlink(lua_State *L) {
	if (symlink(luaL_checkstring(L, 1), luaL_checkstring(L, 2)) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_utimes(lua_State *L) {
	const char *path = luaL_checkstring(L, 1);
	struct timeval tv[2];
	tv[0].tv_sec = tv[1].tv_sec = (time_t)luaL_checkinteger(L, 2);
	tv[0].tv_usec = tv[1].tv_usec = (suseconds_t)(luaL_optinteger(L, 3, 0) / 1000);
	if (utimes(path, tv) != 0) {
		return push_errno(L);
	}
	lua_pushboolean(L, 1);
	return 1;
}

static int l_cpucount(lua_State *L) {
	long n = sysconf(_SC_NPROCESSORS_ONLN);
	lua_pushinteger(L, n > 0 ? n : 1);
	return 1;
}

static int l_enablevt(lua_State *L) {
	(void)L;
	return 0;
}

static int l_setbinary(lua_State *L) {
	(void)L;
	return 0;
}

#endif

/*
 * setmemlimit(bytes) caps the memory Lua allocates: past the limit, allocations fail and Lua
 * raises "not enough memory" in the running code, so a runaway test fails instead of exhausting
 * the machine. It wraps the state's allocator, so it works in compiled code too.
 */
typedef struct {
	lua_Alloc f;
	void *ud;
	size_t used;
	size_t limit;
	/* The limit in force: raised by a quarter after a failure, so the error can unwind. */
	size_t cap;
} MemLimit;

static MemLimit memlimit;

static void *limited_alloc(void *ud, void *ptr, size_t osize, size_t nsize) {
	MemLimit *m = (MemLimit *)ud;
	size_t old = ptr != NULL ? osize : 0;
	if (nsize > old && m->used + (nsize - old) > m->cap) {
		if (m->cap == m->limit) {
			m->cap = m->limit + m->limit / 4;
		}
		return NULL;
	}
	void *p = m->f(m->ud, ptr, osize, nsize);
	if (p != NULL || nsize == 0) {
		m->used = m->used - old + nsize;
	}
	if (m->cap != m->limit && m->used < m->limit) {
		m->cap = m->limit;
	}
	return p;
}

static int l_setmemlimit(lua_State *L) {
	lua_Number limit = luaL_checknumber(L, 1);
	if (memlimit.f == NULL) {
		/* Closing the state unloads this module before its last frees, which go through
		 * limited_alloc, so keep the module loaded for the life of the process. */
#ifdef _WIN32
		HMODULE self;
		GetModuleHandleExA(GET_MODULE_HANDLE_EX_FLAG_FROM_ADDRESS | GET_MODULE_HANDLE_EX_FLAG_PIN, (LPCSTR)&limited_alloc, &self);
#else
		Dl_info info;
		if (dladdr((void *)&limited_alloc, &info) && info.dli_fname != NULL) {
			dlopen(info.dli_fname, RTLD_NOW | RTLD_NODELETE);
		}
#endif
		memlimit.f = lua_getallocf(L, &memlimit.ud);
		/* Count what the state holds already. */
		memlimit.used = (size_t)lua_gc(L, LUA_GCCOUNT, 0) * 1024 + (size_t)lua_gc(L, LUA_GCCOUNTB, 0);
		lua_setallocf(L, limited_alloc, &memlimit);
	}
	memlimit.limit = (size_t)limit;
	memlimit.cap = memlimit.limit;
	return 0;
}

static int l_stat(lua_State *L) {
	return do_stat(L, 1);
}

static int l_lstat(lua_State *L) {
	return do_stat(L, 0);
}

static const luaL_Reg funcs[] = {
	{"stat", l_stat},
	{"lstat", l_lstat},
	{"readdir", l_readdir},
	{"mkdir", l_mkdir},
	{"rmdir", l_rmdir},
	{"unlink", l_unlink},
	{"rename", l_rename},
	{"realpath", l_realpath},
	{"getcwd", l_getcwd},
	{"chdir", l_chdir},
	{"monotime", l_monotime},
	{"walltime", l_walltime},
	{"sleep", l_sleep},
	{"isatty", l_isatty},
	{"exepath", l_exepath},
	{"readlink", l_readlink},
	{"symlink", l_symlink},
	{"utimes", l_utimes},
	{"cpucount", l_cpucount},
	{"enablevt", l_enablevt},
	{"setbinary", l_setbinary},
	{"setmemlimit", l_setmemlimit},
#ifdef _WIN32
	{"open", l_open},
#endif
	{NULL, NULL},
};

LUAMOD_API int luaopen_tscnative(lua_State *L) {
	luaL_newlib(L, funcs);
#ifdef _WIN32
	lua_pushliteral(L, "windows");
#elif defined(__APPLE__)
	lua_pushliteral(L, "darwin");
#else
	lua_pushliteral(L, "linux");
#endif
	lua_setfield(L, -2, "os");
#if defined(__aarch64__) || defined(_M_ARM64)
	lua_pushliteral(L, "arm64");
#else
	lua_pushliteral(L, "amd64");
#endif
	lua_setfield(L, -2, "arch");
	return 1;
}
