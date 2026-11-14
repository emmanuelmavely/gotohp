#!/usr/bin/env python3
"""Generates core/library_requests_gen.go and core/testdata/*.hex.

The request bodies are extracted from xob0t/gpmc (the Python sibling project
of gotohp, which uses the same reverse-engineered mobile API) by parsing
gpmc/gpmc/api.py with ast. The reference hex files are produced by encoding
the extracted bodies with the same blackboxprotobuf library gpmc uses, so the
Go wire encoder in core/pbtree.go can be verified against gpmc byte-for-byte.

Usage:
    python scripts/gen_library_requests.py <path-to-gpmc-clone>

Requires: pip install bbpb
"""

import ast
import os
import sys
from pathlib import Path

import blackboxprotobuf

REPO_ROOT = Path(__file__).resolve().parent.parent

# Test values substituted for the runtime parameters when generating the
# reference hex. The Go tests must use the same values.
TEST_SYNC_TOKEN = "SYNC_TEST_TOKEN"
TEST_RESUME_TOKEN = "RESUME_TEST_TOKEN"
TEST_MEDIA_KEY = "MEDIA_TEST_KEY"
TEST_DEDUP_KEY = "DEDUP_TEST_KEY"
TEST_CLIENT_VERSION = 49029607
TEST_API_VERSION = 28

# api.py function -> (message_types.py typedef, output file stem)
SYNC_TARGETS = {
    "get_library_state": ("GET_LIB_STATE", "library_state"),
    "get_library_page_init": ("GET_LIB_PAGE_INIT", "library_page_init"),
    "get_library_page": ("GET_LIB_PAGE", "library_page"),
}

HEX_TARGETS = {
    "move_remote_media_to_trash": ("MOVE_TO_TRASH", "media_state_trash"),
    "delete_remote_media_permanently": ("DELETE_PERMANENTLY", "media_state_delete"),
    "restore_from_trash": ("RESTORE_FROM_TRASH", "media_state_restore"),
    "get_download_urls": ("GET_DOWNLOAD_URLS", "prepare_download"),
}

# AST placeholder -> (go expression, test value)
VARS = {
    "sync_token": ("syncToken", TEST_SYNC_TOKEN),
    "resume_token": ("resumeToken", TEST_RESUME_TOKEN),
    "media_key": ("mediaKey", TEST_MEDIA_KEY),
    "dedup_keys": ("dedupKeys", [TEST_DEDUP_KEY]),
    "client_version_code": ("clientVersionCode", TEST_CLIENT_VERSION),
    "android_api_version": ("androidAPIVersion", TEST_API_VERSION),
    "android_api_version_str": ("androidAPIVersionStr", str(TEST_API_VERSION)),
}


def extract_proto_bodies(api_path):
    """Finds the `proto_body = {...}` assignment in each target function."""
    tree = ast.parse(api_path.read_text(encoding="utf-8"))
    bodies = {}
    for node in ast.walk(tree):
        if not isinstance(node, ast.FunctionDef) or node.name not in set(SYNC_TARGETS) | set(HEX_TARGETS):
            continue
        for stmt in ast.walk(node):
            if (
                isinstance(stmt, ast.Assign)
                and len(stmt.targets) == 1
                and isinstance(stmt.targets[0], ast.Name)
                and stmt.targets[0].id == "proto_body"
            ):
                bodies[node.name] = evaluate(stmt.value)
                break
        else:
            raise RuntimeError(f"proto_body not found in {node.name}")
    return bodies


def evaluate(node):
    """Evaluates a literal expression to a tagged value.

    Tagged values: ("msg", [(num, value)...]), ("int", n), ("str", s),
    ("bytes", b), ("list", [ints]), ("var", name) for string placeholders,
    ("intvar", name) and ("strvar", name) for typed placeholders.
    """
    if isinstance(node, ast.Constant):
        value = node.value
        if isinstance(value, bool):
            raise RuntimeError("unexpected bool constant")
        if isinstance(value, int):
            return ("int", value)
        if isinstance(value, str):
            return ("str", value)
        if isinstance(value, bytes):
            return ("bytes", value)
        raise RuntimeError(f"unexpected constant type {type(value)}")
    if isinstance(node, ast.Name):
        if node.id not in ("sync_token", "resume_token", "media_key", "dedup_keys"):
            raise RuntimeError(f"unexpected name {node.id}")
        name, _ = VARS[node.id]
        return ("listvar" if node.id == "dedup_keys" else "var", name)
    if isinstance(node, ast.Attribute):
        if node.attr not in ("client_version_code", "android_api_version"):
            raise RuntimeError(f"unexpected attribute {node.attr}")
        return ("intvar", VARS[node.attr][0])
    if isinstance(node, ast.Call):
        func, args = node.func, node.args
        if isinstance(func, ast.Name) and func.id == "str" and len(args) == 1:
            inner = args[0]
            if isinstance(inner, ast.Attribute) and inner.attr == "android_api_version":
                return ("strvar", VARS["android_api_version_str"][0])
        raise RuntimeError(f"unsupported call {ast.dump(node)}")
    if isinstance(node, ast.Dict):
        items = []
        for key, value in zip(node.keys, node.values):
            if not isinstance(key, ast.Constant) or not isinstance(key.value, str):
                raise RuntimeError(f"unexpected dict key {key}")
            items.append((int(key.value), evaluate(value)))
        return ("msg", items)
    if isinstance(node, ast.List):
        values = []
        for item in node.elts:
            kind, payload = evaluate(item)
            if kind != "int":
                raise RuntimeError("list items must be ints")
            values.append(payload)
        return ("list", values)
    raise RuntimeError(f"unsupported node {ast.dump(node)}")


def substitute(value):
    """Replaces placeholders with test values for the blackboxprotobuf encode."""
    kind, payload = value
    if kind in ("var", "strvar", "intvar", "listvar"):
        return VARS[
            {
                "syncToken": "sync_token",
                "resumeToken": "resume_token",
                "mediaKey": "media_key",
                "dedupKeys": "dedup_keys",
                "clientVersionCode": "client_version_code",
                "androidAPIVersion": "android_api_version",
                "androidAPIVersionStr": "android_api_version_str",
            }[payload]
        ][1]
    if kind == "msg":
        return {num: substitute(item) for num, item in payload}
    if kind == "list":
        return list(payload)
    return payload


def go_string(value):
    """Renders a Python str/bytes as a Go string literal."""
    if isinstance(value, bytes):
        value = value.decode("latin-1")
    parts = []
    for ch in value:
        code = ord(ch)
        if code > 0xFF:
            raise RuntimeError("non-latin1 character in body")
        if ch == '"':
            parts.append('\\"')
        elif ch == "\\":
            parts.append("\\\\")
        elif 0x20 <= code <= 0x7E:
            parts.append(ch)
        else:
            parts.append("\\x%02x" % code)
    return '"' + "".join(parts) + '"'


def emit_go(num, value, indent):
    """Renders one tagged value as Go pbNode code."""
    pad = "\t" * indent
    kind, payload = value
    if kind == "msg":
        if not payload:
            return f"{pad}nMsg({num}),\n"
        inner = "".join(emit_go(child_num, child_value, indent + 1) for child_num, child_value in payload)
        return f"{pad}nMsg({num},\n{inner}{pad}),\n"
    if kind == "int":
        return f"{pad}nVarint({num}, {payload}),\n"
    if kind == "intvar":
        return f"{pad}nVarint({num}, {payload}),\n"
    if kind == "str":
        return f"{pad}nStr({num}, {go_string(payload)}),\n"
    if kind == "strvar" or kind == "var":
        return f"{pad}nStr({num}, {payload}),\n"
    if kind == "bytes":
        return f"{pad}nBytes({num}, {go_string(payload)}),\n"
    if kind == "list":
        return f"{pad}nRepeat({num}, {', '.join(str(x) for x in payload)}),\n"
    raise RuntimeError(f"cannot emit {kind}")


SIGNATURES = {
    "libraryStateRequestBody": ["syncToken string"],
    "libraryPageInitRequestBody": ["resumeToken string"],
    "libraryPageRequestBody": ["resumeToken, syncToken string"],
}


def emit_function(name, body_value):
    kind, payload = body_value
    if kind != "msg":
        raise RuntimeError("top-level body must be a message")
    inner = "".join(emit_go(num, value, 2) for num, value in payload)
    params = ", ".join(SIGNATURES[name])
    return (
        f"func {name}({params}) []pbNode {{\n"
        f"\treturn []pbNode{{\n"
        f"{inner}"
        f"\t}}\n"
        f"}}\n"
    )


def main():
    if len(sys.argv) != 2:
        sys.exit(f"usage: {sys.argv[0]} <path-to-gpmc-clone>")
    gpmc_dir = Path(sys.argv[1])
    api_path = gpmc_dir / "gpmc" / "api.py"
    types_path = gpmc_dir / "gpmc" / "message_types.py"

    typedefs = {}
    exec(compile(types_path.read_text(encoding="utf-8"), str(types_path), "exec"), typedefs)

    bodies = extract_proto_bodies(api_path)

    testdata_dir = REPO_ROOT / "core" / "testdata"
    testdata_dir.mkdir(parents=True, exist_ok=True)

    # Reference hex, encoded with the same library gpmc uses.
    for func_name, (typedef_name, file_stem) in list(SYNC_TARGETS.items()) + list(HEX_TARGETS.items()):
        encoded = blackboxprotobuf.encode_message(substitute(bodies[func_name]), typedefs[typedef_name])
        (testdata_dir / f"{file_stem}.hex").write_text(encoded.hex() + "\n", encoding="ascii")
        print(f"wrote core/testdata/{file_stem}.hex ({len(encoded)} bytes)")

    # Go tree builders for the three library-sync requests.
    gen = [
        "// Code generated from xob0t/gpmc gpmc/api.py request bodies by",
        "// scripts/gen_library_requests.py. DO NOT EDIT.",
        "",
        "package core",
        "",
        "// libraryStateRequestBody builds the GetLibraryState (library sync",
        "// state) request body. gpmc calls this CURRENT_SYNC.",
        f"\n{emit_function('libraryStateRequestBody', bodies['get_library_state'])}",
        "// libraryPageInitRequestBody builds the initial library page request.",
        "// gpmc calls this INITIAL_RESUME.",
        f"\n{emit_function('libraryPageInitRequestBody', bodies['get_library_page_init'])}",
        "// libraryPageRequestBody builds the delta library page request.",
        "// gpmc calls this DELTA_RESUME.",
        f"\n{emit_function('libraryPageRequestBody', bodies['get_library_page'])}",
    ]
    out_path = REPO_ROOT / "core" / "library_requests_gen.go"
    # newline="" keeps the file byte-identical to what gofumpt produces.
    with open(out_path, "w", encoding="utf-8", newline="") as fh:
        fh.write("\n".join(gen))
    print(f"wrote {out_path.relative_to(REPO_ROOT)}")


if __name__ == "__main__":
    main()
