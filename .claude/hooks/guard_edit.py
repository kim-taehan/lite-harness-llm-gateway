#!/usr/bin/env python3
"""PreToolUse(Edit|Write|MultiEdit) 훅. 편집 결과 파일 전체를 만들어 보고 개발 표준·아키텍처 위반이면 막는다.

종료 코드 2 + stderr 는 Claude 에게 이유로 전달되고 편집은 적용되지 않는다.
규칙의 근거는 CLAUDE.md(코딩 원칙·코틀린 관례)와 GATEWAY_DESIGN.md(3장 모듈 규칙, 8장 스키마, 10장 폐쇄망)다.
"""
import json
import os
import re
import subprocess
import sys

BASE_PACKAGE = "ai.airgap.gateway"
MODULES = ("inference", "auth", "limits", "admin", "core")
# 설계 3장: admin → inference·auth·limits → core, inference → auth·limits → core
ALLOWED_DEPS = {
    "core": set(),
    "auth": {"core"},
    "limits": {"core"},
    "inference": {"auth", "limits", "core"},
    "admin": {"inference", "auth", "limits", "core"},
}
VAGUE_PACKAGE_NAMES = {"util", "utils", "common", "helper", "helpers", "manager", "managers", "misc"}
PRIVATE_HOST = re.compile(
    r"^(localhost|127\.|10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|0\.0\.0\.0|\[::1\]|[^/:]*\.(local|internal|svc|cluster\.local)(:|/|$))"
)
MIGRATION_DIR = "src/main/resources/db/migration/"


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

    if rel.endswith(".kt") and rel.startswith("src/"):
        violations = check_kotlin(rel, content)
    elif rel.startswith(MIGRATION_DIR):
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


# ---------- Kotlin ----------

def check_kotlin(rel, content):
    is_main = rel.startswith("src/main/")
    code = strip_comments_and_strings(content)
    violations = []

    package = find_package(content)
    module = module_of(package)
    violations += check_package(rel, package, module)
    violations += check_dependencies(code, package, module)

    if "!!" in code:
        violations.append("`!!` 를 쓰지 않는다. `?.`·`?:`·`requireNotNull(이유)` 를 쓴다")
    if re.search(r"\bGlobalScope\b", code):
        violations.append("`GlobalScope` 금지. 요청 안은 요청 코루틴, 백그라운드는 애플리케이션 스코프에 둔다")

    if is_main:
        if re.search(r"\brunBlocking\b", code):
            violations.append("`runBlocking` 은 운영 코드에서 쓰지 않는다(이벤트 루프 블로킹)")
        if re.search(r"\bThread\.sleep\b", code):
            violations.append("`Thread.sleep` 금지. `delay` 를 쓴다")
        if re.search(r"@Autowired\b", code):
            violations.append("필드·세터 주입(`@Autowired`) 금지. 생성자 주입을 쓴다")
        if re.search(r"\bimport\s+java\.sql\.|\bJdbcTemplate\b|\bDriverManager\b|\bDataSource\b", code):
            violations.append("런타임 DB 접근은 R2DBC 만 쓴다. JDBC 는 Flyway 기동 마이그레이션 전용이다(설계 2장)")
        if re.search(r"catch\s*\(\s*\w+\s*:\s*(Exception|Throwable)\s*\)", code) and "CancellationException" not in code:
            violations.append(
                "`catch (e: Exception|Throwable)` 이 CancellationException 까지 삼킨다. "
                "CancellationException 을 먼저 잡아 다시 던지거나 더 좁은 예외를 잡는다"
            )
        violations += check_external_urls(content)
    return violations


def find_package(content):
    m = re.search(r"^\s*package\s+([\w.]+)", content, re.M)
    return m.group(1) if m else None


def module_of(package):
    if not package or not package.startswith(BASE_PACKAGE + "."):
        return None
    return package[len(BASE_PACKAGE) + 1:].split(".")[0]


def check_package(rel, package, module):
    if package is None:
        return ["package 선언이 없다"]
    violations = []
    expected_dir = package.replace(".", "/")
    if "/" + expected_dir + "/" not in "/" + rel:
        violations.append(f"package `{package}` 와 파일 위치가 다르다. 경로에 `{expected_dir}/` 가 있어야 한다")
    if package != BASE_PACKAGE and not package.startswith(BASE_PACKAGE + "."):
        violations.append(f"패키지는 `{BASE_PACKAGE}` 아래에 둔다")
    elif module is not None and module not in MODULES:
        violations.append(f"모듈 `{module}` 은 설계에 없다. 모듈은 {', '.join(MODULES)} 뿐이다(새 모듈은 설계 문서부터)")
    segments = package.split(".")
    vague = [s for s in segments if s in VAGUE_PACKAGE_NAMES]
    if vague:
        violations.append(f"뭉뚱그린 패키지 이름 `{vague[0]}` 금지. 기능별로 나눈다")
    return violations


def check_dependencies(code, package, module):
    """import 와 완전한 이름 참조 모두에서 다른 모듈 참조를 찾는다."""
    violations = []
    refs = set(re.findall(r"\b" + re.escape(BASE_PACKAGE) + r"\.(\w+)((?:\.\w+)*)", code))
    for target, rest in sorted(refs):
        if target not in MODULES or target == module:
            continue
        internal = ".internal" in rest + "."
        if internal:
            violations.append(f"`{target}.internal` 은 `{target}` 모듈만 쓴다. 공개 API(모듈 최상위 패키지)를 쓴다")
        if module is None:
            # 루트 패키지(애플리케이션 클래스·앱 수준 테스트)는 공개 API 만 쓰면 된다
            continue
        if target not in ALLOWED_DEPS.get(module, set()):
            allowed = ", ".join(sorted(ALLOWED_DEPS.get(module, set()))) or "없음"
            violations.append(f"`{module}` → `{target}` 의존 금지. `{module}` 이 기댈 수 있는 모듈: {allowed}(설계 3장)")
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
    """문자열 안의 // 를 주석으로 보지 않도록 한 글자씩 읽는다. 지운 자리는 공백·빈 문자열로 둔다."""
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
        elif source.startswith('"""', i):
            end = source.find('"""', i + 3)
            end = n if end < 0 else end + 3
            out.append(source[i:end] if keep_strings else '""')
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


# ---------- Flyway ----------

def check_migration(project, rel, content):
    violations = []
    name = os.path.basename(rel)
    if not re.match(r"^V\d+__[a-z0-9_]+\.sql$", name):
        violations.append("마이그레이션 파일 이름은 `V<번호>__<snake_case>.sql`")
    if is_committed(project, rel):
        violations.append("이미 커밋된 마이그레이션은 고치지 않는다. 새 버전 파일을 추가한다(설계 8.1)")
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
