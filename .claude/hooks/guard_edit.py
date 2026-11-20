#!/usr/bin/env python3
"""PreToolUse(Edit|Write|MultiEdit) 훅. 편집 결과 파일 전체를 만들어 보고 개발 표준·아키텍처 위반이면 막는다.

종료 코드 2 + stderr 는 Claude 에게 이유로 전달되고 편집은 적용되지 않는다.
규칙의 근거는 CLAUDE.md(Go 관례)와 GATEWAY_DESIGN.md(3장 모듈 규칙, 8장 스키마, 10장 폐쇄망)다.
정확한 경계 검사는 internal/app/architecture_test.go 이고, 이 훅은 편집 시점에 빨리 막는 앞단이다.
"""
import json
import os
import re
import subprocess
import sys

MODULE_PATH = "llm-gateway"
# 설계 3장: app(조립) → admin → inference → auth·limits → core. architecture_test.go 와 같은 표
ALLOWED_DEPS = {
    "core": set(),
    "auth": {"core"},
    "limits": {"core"},
    "inference": {"auth", "limits", "core"},
    "admin": {"inference", "auth", "limits", "core"},
    "app": {"admin", "inference", "auth", "limits", "core"},
}
VAGUE_NAMES = {"util", "utils", "common", "helper", "helpers", "manager", "managers", "misc", "shared"}
PRIVATE_HOST = re.compile(
    r"^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|0\.0\.0\.0|\[::1\]|[^/:]*\.(local|internal|svc|cluster\.local)(:|/|$))"
)


def main():
    event = json.load(sys.stdin)
    tool_input = event.get("tool_input", {})
    path = tool_input.get("file_path", "")
    project = os.environ.get("CLAUDE_PROJECT_DIR") or event.get("cwd") or os.getcwd()
    rel = os.path.relpath(path, project) if path else ""
    if rel.startswith(".."):
        return 0

    content = resulting_content(event.get("tool_name"), tool_input, path)
    if content is None:
        return 0

    if rel.endswith(".go") and (rel.startswith("internal/") or rel.startswith("cmd/")):
        violations = check_go(rel, content)
    elif rel.endswith(".sql") and "/migrations/" in "/" + rel:
        violations = check_migration(project, rel, content)
    else:
        violations = []

    if violations:
        sys.stderr.write(
            "개발 표준 훅이 이 편집을 막았다 (" + rel + "):\n"
            + "\n".join("- " + v for v in violations)
            + "\n규칙: CLAUDE.md, GATEWAY_DESIGN.md 3장. 규칙이 틀렸다고 판단되면 우회하지 말고 사용자에게 알린다.\n"
        )
        return 2
    return 0


def resulting_content(tool_name, tool_input, path):
    """편집이 적용된 뒤의 파일 전체. 만들 수 없으면 None(검사하지 않고 통과)."""
    if tool_name == "Write":
        return tool_input.get("content", "")
    try:
        with open(path, encoding="utf-8") as f:
            current = f.read()
    except OSError:
        return None
    edits = tool_input.get("edits") if tool_name == "MultiEdit" else [tool_input]
    for edit in edits or []:
        old, new = edit.get("old_string", ""), edit.get("new_string", "")
        if old not in current:
            return None  # 편집 자체가 실패할 것이다. 판단은 도구에 맡긴다
        current = current.replace(old, new) if edit.get("replace_all") else current.replace(old, new, 1)
    return current


# ---------- Go ----------

def check_go(rel, content):
    # 시험 파일과 시험 도우미 패키지(이름이 test 로 끝나는 폴더, 예: core/dbtest)는 관례 검사를 하지 않는다
    is_test = rel.endswith("_test.go") or os.path.basename(os.path.dirname(rel)).endswith("test")
    parts = rel.split("/")
    module = parts[1] if parts[0] == "internal" and len(parts) > 2 else ("cmd" if parts[0] == "cmd" else None)
    code = strip_comments_and_strings(content)
    violations = []

    if parts[0] == "internal" and len(parts) > 2 and module not in ALLOWED_DEPS:
        violations.append(f"모듈 `internal/{module}` 은 설계에 없다. 모듈은 {', '.join(ALLOWED_DEPS)} 뿐이다(새 모듈은 설계 문서부터)")
    vague = [p for p in parts[:-1] if p in VAGUE_NAMES]
    if vague:
        violations.append(f"뭉뚱그린 패키지 이름 `{vague[0]}` 금지. 기능별로 나눈다")
    violations += check_imports(content, module)

    if is_test:
        return violations
    if re.search(r"\bcontext\.(Background|TODO)\(\)", code) and module not in ("app", "cmd"):
        violations.append(
            "`context.Background()`·`TODO()` 는 조립 지점(internal/app, cmd)에서만 쓴다. "
            "요청·작업의 ctx 를 받아 넘겨야 클라이언트가 끊을 때 업스트림·DB 까지 취소된다(설계 4.2)"
        )
    if re.search(r"\btime\.Sleep\(", code):
        violations.append("`time.Sleep` 금지. ctx 와 함께 `select { case <-ctx.Done(): case <-timer.C: }` 로 기다린다")
    if re.search(r"\bhttp\.(DefaultClient|Get|Post|Head|PostForm)\b", code):
        violations.append("`http.DefaultClient`·`http.Get` 류 금지(타임아웃이 없다). 타임아웃을 정한 `*http.Client` 를 주입한다")
    if re.search(r'^\s*"log"\s*$', import_block(content), re.M):
        violations.append('표준 `log` 패키지 금지. 구조 로그 `log/slog` 를 쓴다(설계 11.3)')
    violations += check_external_urls(content)
    return violations


def import_block(content):
    m = re.search(r"^import\s*\((.*?)^\)", content, re.S | re.M)
    singles = re.findall(r'^import\s+(?:\w+\s+)?("[^"]+")', content, re.M)
    return (m.group(1) if m else "") + "\n" + "\n".join(singles)


def check_imports(content, module):
    violations = []
    for target in re.findall(r'"' + re.escape(MODULE_PATH) + r'/([^"]+)"', import_block(content)):
        segments = target.split("/")
        if module == "cmd":
            if target != "internal/app":
                violations.append(f"cmd 는 `internal/app` 만 가져온다 (가져온 것: {target})")
            continue
        if segments[0] != "internal" or len(segments) < 2 or module is None:
            continue
        to = segments[1]
        if to != module and to not in ALLOWED_DEPS.get(module, set()):
            allowed = ", ".join(sorted(ALLOWED_DEPS.get(module, set()))) or "없음"
            violations.append(f"`{module}` → `{to}` 의존 금지. `{module}` 이 가져올 수 있는 모듈: {allowed}(설계 3장)")
    return violations


def check_external_urls(content):
    violations = []
    for host in re.findall(r"https?://([^\s\"'`)]+)", strip_comments(content)):
        if not PRIVATE_HOST.match(host):
            violations.append(f"외부 주소 `{host}` 가 코드에 있다. 폐쇄망이라 런타임 외부 호출 0(설계 10장). 주소는 설정으로 받는다")
    return violations


def strip_comments(source):
    return _scan(source, keep_strings=True)


def strip_comments_and_strings(source):
    return _scan(source, keep_strings=False)


def _scan(source, keep_strings):
    """문자열 안의 // 를 주석으로 보지 않도록 한 글자씩 읽는다. Go 의 "…", `…`, '…' 를 다룬다."""
    out = []
    i, n = 0, len(source)
    while i < n:
        if source.startswith("//", i):
            end = source.find("\n", i)
            i = n if end < 0 else end
        elif source.startswith("/*", i):
            end = source.find("*/", i + 2)
            i = n if end < 0 else end + 2
            out.append(" ")
        elif source[i] == "`":
            end = source.find("`", i + 1)
            end = n if end < 0 else end + 1
            out.append(source[i:end] if keep_strings else "``")
            i = end
        elif source[i] in "\"'":
            quote, j = source[i], i + 1
            while j < n and source[j] != quote and source[j] != "\n":
                j += 2 if source[j] == "\\" else 1
            end = min(j + 1, n)
            out.append(source[i:end] if keep_strings else quote * 2)
            i = end
        else:
            out.append(source[i])
            i += 1
    return "".join(out)


# ---------- 마이그레이션 ----------

def check_migration(project, rel, content):
    violations = []
    name = os.path.basename(rel)
    if not re.match(r"^\d{4}_[a-z0-9_]+\.sql$", name):
        violations.append("마이그레이션 파일 이름은 `<4자리 번호>_<snake_case>.sql` (예: 0001_identity.sql)")
    if is_committed(project, rel):
        violations.append("이미 커밋된 마이그레이션은 고치지 않는다. 새 번호 파일을 추가한다(설계 8.1)")
    sql = re.sub(r"--[^\n]*", "", content)
    if re.search(r"\b(UPDATE\s+\w+\s+SET|DELETE\s+FROM|INSERT\s+INTO)\b", sql, re.I):
        violations.append("마이그레이션은 스키마만 바꾼다. 행을 쓰는 INSERT·UPDATE·DELETE 금지(설계 8.1)")
    return violations


def is_committed(project, rel):
    result = subprocess.run(
        ["git", "-C", project, "cat-file", "-e", "HEAD:" + rel],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
    )
    return result.returncode == 0


if __name__ == "__main__":
    sys.exit(main())
